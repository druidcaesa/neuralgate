// Copyright 2026 FanYaNan. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
	"github.com/druidcaesa/neuralgate/pkg/admin"
	"github.com/druidcaesa/neuralgate/pkg/config"
	"github.com/druidcaesa/neuralgate/pkg/core"
	"github.com/druidcaesa/neuralgate/pkg/docsui"
	"github.com/druidcaesa/neuralgate/pkg/license"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	golumberjack "gopkg.in/natefinch/lumberjack.v2"
)

func main() {
	// machine-id 子命令:打印本机机器码(供授权设备绑定取码),无需配置/授权
	if len(os.Args) > 1 && os.Args[1] == "machine-id" {
		os.Exit(machineIDCommand(os.Stdout))
	}

	// 1. 解析命令行参数
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	showVersion := flag.Bool("version", false, "显示版本信息")
	flag.Parse()

	if *showVersion {
		fmt.Printf("NeuralGate %s (edition=%s, build=%s, commit=%s)\n",
			core.Version, edition, core.BuildTime, core.GitCommit)
		return
	}

	// 2. 加载配置文件
	cfg, err := config.Load(*configPath)
	if err != nil {
		logFatal("加载配置失败", err)
	}
	if err := cfg.Validate(); err != nil {
		logFatal("配置校验失败", err)
	}
	if err := core.SetTrustedProxies(cfg.Security.TrustedProxies); err != nil {
		logFatal("可信代理配置非法", err)
	}
	logger := initLogger(cfg.Log)
	defer logger.Sync()

	logger.Info("NeuralGate 启动",
		zap.String("version", core.Version),
		zap.String("edition", edition),
		zap.String("build_time", core.BuildTime),
		zap.String("git_commit", core.GitCommit),
	)

	// 3. 初始化插件工厂（BuildTag 决定实现）
	factory := newPluginFactory()
	storage := factory.CreateStorage()
	if err := storage.Init(map[string]any{
		"driver":      cfg.Storage.Driver,
		"dsn":         cfg.Storage.DSN,
		"encrypt_key": cfg.Storage.EncryptKey,
	}); err != nil {
		logger.Fatal("存储初始化失败", zap.Error(err))
	}
	auditor := factory.CreateAuditor()
	if err := auditor.Init(plugin.AuditConfig{
		QueueSize:     cfg.Audit.QueueSize,
		WorkerCount:   cfg.Audit.WorkerCount,
		BatchSize:     cfg.Audit.BatchSize,
		FlushInterval: cfg.Audit.FlushInterval,
		EnableSHA256:  cfg.Audit.EnableSHA256,
		RetentionDays: cfg.Audit.RetentionDays,
	}); err != nil {
		logger.Fatal("审计初始化失败", zap.Error(err))
	}
	rateLimiter := factory.CreateRateLimiter()
	if err := rateLimiter.Init(map[string]any{
		"default_rps": cfg.RateLimit.DefaultRPS,
		"default_tpm": cfg.RateLimit.DefaultTPM,
	}); err != nil {
		logger.Fatal("限流初始化失败", zap.Error(err))
	}
	logger.Info("插件工厂初始化完成", zap.String("storage_driver", cfg.Storage.Driver))

	// 3.5 统一留存(OSS 基础能力,与授权无关):每小时清理超期日志,多副本幂等
	stopRetention := startLogRetention(storage, cfg.Audit.RetentionDays, logger)

	// 4. 从存储加载模型配置（内存存储返回空表，仅打印数量）
	models, total, err := storage.ListModelConfigs(1, 100)
	if err != nil {
		logger.Warn("加载模型配置失败", zap.Error(err))
	} else {
		logger.Info("加载模型配置", zap.Int64("total", total), zap.Int("loaded", len(models)))
	}

	// 5. 初始化模型适配器注册中心
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	registry.Register(adapter.NewQwenAdapter())
	registry.Register(adapter.NewZhipuAdapter())
	registry.Register(adapter.NewDeepSeekAdapter())
	registry.Register(adapter.NewAnthropicAdapter())

	// 6. 初始化代理内核
	// acceptor 的创建在步骤10 setupPrivacy 之后：pipeline.Build 快照中间件链，Use 晚于 Build 不生效
	pipeline := core.NewPipeline(storage, rateLimiter, auditor, registry)
	proxyCore := core.NewProxyCore(pipeline, registry).WithLogger(logger)
	ipf := core.NewIPFilter(cfg.IPFilter.Mode, cfg.IPFilter.Whitelist, cfg.IPFilter.Blacklist)

	// 7. 授权校验（失败软降级 OSS）与管理后台初始化
	validator := factory.CreateLicenseValidator()
	effectiveEdition := edition
	var gate core.LicenseGate = core.NopGate()
	licenseOverview := &admin.LicenseOverview{Status: "oss", Message: "开源版本，无授权信息"}

	if validator != nil {
		licenseOverview.Status = "missing"
		licenseOverview.Message = "未检测到授权文件"
		info, err := validator.LoadLicense(cfg.License.FilePath)
		if err != nil {
			effectiveEdition = "oss"
			logger.Warn("未检测到授权文件，以开源模式运行",
				zap.String("path", cfg.License.FilePath), zap.Error(err))
		} else if ok, verr := validator.Validate(info); !ok {
			effectiveEdition = "oss"
			logger.Warn("授权无效或已过期，降级为开源模式运行", zap.Error(verr))
			licenseOverview.Status = licenseStatus(verr)
			licenseOverview.Message = verr.Error()
			licenseOverview.Info = info // 过期/无效授权仍尽量展示业务字段
		} else {
			effectiveEdition = "enterprise"
			gate = validator
			licenseOverview.Status = "valid"
			licenseOverview.Message = ""
			licenseOverview.Info = info
			logger.Info("授权校验通过",
				zap.String("customer", info.CustomerName),
				zap.Time("expires_at", info.ExpiresAt),
				zap.Strings("features", info.Features))
		}
	}
	logger.Info("功能门控初始化完成",
		zap.String("effective_edition", effectiveEdition),
		zap.Bool("gate_enabled", gate != core.LicenseGate(core.NopGate())))

	adminServer := admin.NewAdminServer(storage, logger, effectiveEdition, rateLimiter, licenseOverview)
	// 下发代理服务公开地址元信息(顶栏「接口说明」入口):scheme 按 TLS 推导,端口从代理监听地址解析
	adminServer.SetGatewayMeta(proxyScheme(cfg.TLS.Enabled), proxyPort(cfg.Server.ProxyAddr))
	// 授权上传管理（enterprise 注入独立校验器；OSS 为空操作，上传接口恒 501）
	setupLicenseManager(*cfg, adminServer, logger)
	// 管理会话装配：共享密钥/TTL(多副本配同一 admin.session_secret 即会话互认，重启不失效)
	adminServer.ConfigureSessions(cfg.Admin.SessionSecret, cfg.Admin.SessionTTL)
	// CORS 白名单（空=同源部署不发送跨域头）；首个管理员账号缺位时引导创建
	if len(cfg.Admin.AllowedOrigins) > 0 {
		adminServer.EnableAuth(nil, cfg.Admin.AllowedOrigins)
	}
	if err := admin.EnsureBootstrapAdmin(storage, logger, cfg.Admin.BootstrapPassword); err != nil {
		logger.Fatal("初始化管理员账号失败", zap.Error(err))
	}

	// 11. 权限体系（rbac 门控；管理面行为开关，须在服务监听前注入）
	if start, reason := shouldStartRBAC(gate, cfg.RBAC.Enabled); !start {
		logger.Info("权限体系未启用", zap.String("reason", reason))
	} else {
		adminServer.EnableRBAC(true)
		logger.Info("权限体系已启用")
	}

	// 12. 合规运维（compliance 门控，接线随构建版本；报表调度 + 手动补生成注入）
	stopCompliance := setupCompliance(gate, *cfg, storage, logger, adminServer)

	// 13. MCP 中继（通道 OSS+ 恒可用；审计随构建版本与 mcp_audit 门控）
	// 须在 acceptor 创建（pipeline.Build 快照链）之前挂载
	pipeline.SetMCPRelay(buildMCPRelay(gate, *cfg, storage, auditor, logger))
	logger.Info("MCP 中继已就绪")

	// 14. 分布式限流（distributed_ratelimit 门控，接线随构建版本；
	// 未启用/降级时沿用本地实现）。同样必须在 Build 快照前完成替换
	rateLimiter = setupDistributedRateLimit(gate, *cfg, rateLimiter, logger)
	pipeline.SetRateLimiter(rateLimiter)

	// 8. 审计日志外推（audit_stream 门控）
	exporter := factory.CreateExporter()
	exportStarted := false
	if exporter != nil {
		if start, reason := shouldStartExport(gate, cfg.Export.Enabled); !start {
			logger.Info("审计日志外推未启用", zap.String("reason", reason))
		} else if err := exporter.Init(map[string]any{
			"type":           cfg.Export.Type,
			"endpoint":       cfg.Export.Endpoint,
			"api_key":        cfg.Export.APIKey,
			"topic":          cfg.Export.Topic,
			"batch_size":     cfg.Export.BatchSize,
			"flush_interval": cfg.Export.FlushInterval,
			"logger":         logger,
		}); err != nil {
			logger.Warn("审计日志外推启动失败", zap.Error(err))
		} else {
			exportStarted = true
			logger.Info("审计日志外推已启用",
				zap.String("type", cfg.Export.Type),
				zap.Duration("flush_interval", cfg.Export.FlushInterval))
		}
	}

	// 9. 审计防篡改（tamper_proof 门控，接线随构建版本）
	stopTamper := setupTamper(gate, auditor, storage, cfg.Audit, logger)

	// 10. 数据隐私合规（privacy 门控，接线随构建版本；无后台任务故无需停止函数）
	setupPrivacy(gate, *cfg, pipeline, auditor, storage, logger)

	metrics := core.NewMetrics()
	pipeline.Use(core.ObservabilityMiddleware(metrics, logger))

	acceptor := core.NewAcceptor(proxyCore.Handler(), ipf)
	// 外层指标包裹:覆盖一切到达代理的请求(含鉴权/限流拒绝),见 pkg/core/metrics.go
	proxyHandler := metrics.WrapOuter(acceptor.Handler())
	// /metrics 在管道外层伺服: 免鉴权且不被路由中间件 404(运维采集端点)
	rootHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			core.ServeMetrics(metrics, w, r)
			return
		}
		// /readyz 就绪探针(依赖感知):storage 存活 + 非排空 → 200,否则 503。
		// 与 /healthz(链内纯存活)语义区分;同 /metrics 置于外层免鉴权供 LB 探测
		if r.URL.Path == "/readyz" {
			core.HandleReady(w, r, storage)
			return
		}
		// /docs 公开接口说明页:免鉴权、不落路由中间件,与 /metrics 同层(页面纯静态,无配置回显)
		if docsui.Serve(w, r) {
			return
		}
		proxyHandler.ServeHTTP(w, r)
	})

	// 8. 启动双服务（并发）
	tlsHandler := core.NewTLSHandler(cfg.TLS.Enabled, cfg.TLS.CertFile, cfg.TLS.KeyFile, cfg.TLS.MinVersion)
	tlsConf, err := tlsHandler.TLSConfig()
	if err != nil {
		logger.Fatal("TLS 配置加载失败", zap.Error(err))
	}
	proxyServer := &http.Server{
		Addr:        cfg.Server.ProxyAddr,
		Handler:     rootHandler,
		ReadTimeout: cfg.Server.ReadTimeout,
		// 写超时不在 server 层设置：统一值无法兼顾长流式响应，
		// 由 proxy handler 按请求类型经 ResponseController 设置写截止时间
		WriteTimeout:   0,
		IdleTimeout:    cfg.Server.IdleTimeout,
		MaxHeaderBytes: cfg.Server.MaxHeaderBytes,
		TLSConfig:      tlsConf, // nil 时等同普通 HTTP
		// ConnState 回调：追踪连接状态（当前为空实现）
		ConnState: func(c net.Conn, state http.ConnState) {},
	}
	adminHTTPServer := &http.Server{
		Addr:         cfg.Server.AdminAddr,
		Handler:      adminServer.Router(),
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}

	errCh := make(chan error, 2)
	go func() {
		logger.Info("代理服务启动", zap.String("addr", cfg.Server.ProxyAddr), zap.Bool("tls", tlsConf != nil))
		var serveErr error
		if tlsConf != nil {
			serveErr = proxyServer.ListenAndServeTLS("", "") // 证书已在 TLSConfig
		} else {
			serveErr = proxyServer.ListenAndServe()
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- fmt.Errorf("代理服务: %w", serveErr)
		}
	}()
	go func() {
		logger.Info("管理后台启动", zap.String("addr", cfg.Server.AdminAddr))
		if err := adminHTTPServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("管理后台: %w", err)
		}
	}()

	// 9. 信号监听（优雅关闭）
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Info("收到退出信号", zap.String("signal", sig.String()))
	case err := <-errCh:
		logger.Fatal("服务异常退出", zap.Error(err))
	}

	// 10. 排空标记:置位后 /readyz 转 503,先让 LB 摘除本副本,再断存量连接
	core.SetDraining(true)
	logger.Info("进入排空状态,就绪探针将返回 503")

	// 11. Shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := proxyServer.Shutdown(shutdownCtx); err != nil {
		logger.Warn("代理服务关闭异常", zap.Error(err))
	} else {
		logger.Info("代理服务已关闭", zap.String("addr", cfg.Server.ProxyAddr))
	}
	if err := adminHTTPServer.Shutdown(shutdownCtx); err != nil {
		logger.Warn("管理后台关闭异常", zap.Error(err))
	} else {
		logger.Info("管理后台已关闭", zap.String("addr", cfg.Server.AdminAddr))
	}
	if stopTamper != nil {
		stopTamper() // 先停校验/清理任务，再落库尾部日志
	}
	if stopCompliance != nil {
		stopCompliance() // 停报表调度循环，避免关闭中的存储被继续查询
	}
	if err := auditor.Shutdown(); err != nil {
		logger.Warn("审计管道关闭异常", zap.Error(err))
	} else {
		logger.Info("审计管道已关闭")
	}
	if exportStarted {
		exporter.Close() // 最终一轮拉取兜住 auditor.Shutdown 落库的尾部日志
	}
	if stopRetention != nil {
		stopRetention() // 留存 worker 先停,再关存储
	}
	if err := storage.Close(); err != nil {
		logger.Warn("存储关闭异常", zap.Error(err))
	} else {
		logger.Info("存储已关闭")
	}
	logger.Info("NeuralGate 已退出")
}

// startLogRetention 启动统一留存 worker：每小时清理各日志表早于 audit.retention_days 的记录。
// 留存语义:retentionDays > 0 启用(0 由 applyDefaults 补为 90);<=0 不启动(-1 显式禁用)。
// 这是 OSS 基础能力,与 tamper 授权无关,OSS/enterprise 共用。
// 多副本安全:删除幂等、分批内部收敛,两副本并发执行重复命中计数为 0,无需分布式锁。
// 返回停止函数,须在 storage.Close 之前调用;未启用时返回 nil。
func startLogRetention(storage plugin.StoragePlugin, retentionDays int, logger *zap.Logger) func() {
	if retentionDays <= 0 {
		logger.Info("审计留存未启用", zap.Int("retention_days", retentionDays))
		return nil
	}
	retention := time.Duration(retentionDays) * 24 * time.Hour
	run := func() {
		cutoff := time.Now().Add(-retention)
		checks := []struct {
			name string
			fn   func(time.Time) (int64, error)
		}{
			{"audit_logs", storage.DeleteAuditLogsBefore},
			{"security_events", storage.DeleteSecurityEventsBefore},
			{"mcp_audit_logs", storage.DeleteMCPAuditLogsBefore},
			{"admin_operation_logs", storage.DeleteOperationLogsBefore},
		}
		for _, c := range checks {
			n, err := c.fn(cutoff)
			if err != nil {
				logger.Warn("留存清理失败", zap.String("table", c.name), zap.Error(err))
				continue
			}
			if n > 0 {
				logger.Info("留存清理完成", zap.String("table", c.name), zap.Int64("deleted", n))
			}
		}
	}
	// 单轮 panic 不应带崩 worker：记录后下一轮继续
	tick := func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("留存 worker panic", zap.Any("panic", r))
			}
		}()
		run()
	}

	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		tick() // 启动即清一轮,便于验收与启动留痕
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				tick()
			}
		}
	}()
	logger.Info("审计留存已启用", zap.Int("retention_days", retentionDays))
	return func() {
		close(stopCh)
		<-doneCh
	}
}

// shouldStartExport 判断外推启动条件（配置启用 + 授权含 audit_stream）；不满足给出原因
func shouldStartExport(gate core.LicenseGate, enabled bool) (bool, string) {
	if !enabled {
		return false, "配置未启用(enabled=false)"
	}
	if !gate.HasFeature(license.FeatureAuditStream) {
		return false, "授权未包含 audit_stream 功能"
	}
	return true, ""
}

// shouldStartTamper 判断防篡改启动条件（配置启用 + 授权含 tamper_proof）；不满足给出原因
func shouldStartTamper(gate core.LicenseGate, enabled bool) (bool, string) {
	if !enabled {
		return false, "配置未启用(enable_sha256=false)"
	}
	if !gate.HasFeature(license.FeatureTamperProof) {
		return false, "授权未包含 tamper_proof 功能"
	}
	return true, ""
}

// shouldStartPrivacy 判断隐私防护启动条件（配置启用 + 授权含 privacy）；不满足给出原因
func shouldStartPrivacy(gate core.LicenseGate, enabled bool) (bool, string) {
	if !enabled {
		return false, "配置未启用(privacy.enabled=false)"
	}
	if !gate.HasFeature(license.FeaturePrivacy) {
		return false, "授权未包含 privacy 功能"
	}
	return true, ""
}

// shouldStartRBAC 判断权限体系启动条件（配置启用 + 授权含 rbac）；不满足给出原因
func shouldStartRBAC(gate core.LicenseGate, enabled bool) (bool, string) {
	if !enabled {
		return false, "配置未启用(rbac.enabled=false)"
	}
	if !gate.HasFeature(license.FeatureRBAC) {
		return false, "授权未包含 rbac 功能"
	}
	return true, ""
}

// shouldStartCompliance 判断合规运维启动条件（配置启用 + 授权含 compliance）；不满足给出原因
func shouldStartCompliance(gate core.LicenseGate, enabled bool) (bool, string) {
	if !enabled {
		return false, "配置未启用(compliance.enabled=false)"
	}
	if !gate.HasFeature(license.FeatureCompliance) {
		return false, "授权未包含 compliance 功能"
	}
	return true, ""
}

// shouldStartMCPAudit 判断 MCP 调用审计启动条件（配置启用 + 授权含 mcp_audit）；不满足给出原因。
// 中继通道本身不受此门控——OSS+ 恒可用
func shouldStartMCPAudit(gate core.LicenseGate, enabled bool) (bool, string) {
	if !enabled {
		return false, "配置未启用(mcp_audit.enabled=false)"
	}
	if !gate.HasFeature(license.FeatureMCPAudit) {
		return false, "授权未包含 mcp_audit 功能"
	}
	return true, ""
}

// shouldStartDistributedRateLimit 判断分布式限流启动条件（配置启用 + 授权含
// distributed_ratelimit）；未满足沿用本地限流
func shouldStartDistributedRateLimit(gate core.LicenseGate, enabled bool) (bool, string) {
	if !enabled {
		return false, "配置未启用(rate_limit.distributed.enabled=false)"
	}
	if !gate.HasFeature(license.FeatureDistributedRateLimit) {
		return false, "授权未包含 distributed_ratelimit 功能"
	}
	return true, ""
}

// licenseStatus 将验签失败原因映射为后台展示的授权状态（过期单列，其余归为无效）
func licenseStatus(err error) string {
	if errors.Is(err, license.ErrExpired) {
		return "expired"
	}
	return "invalid"
}

// initLogger 按配置初始化 zap 日志
func initLogger(cfg config.LogConfig) *zap.Logger {
	level := zap.NewAtomicLevel()
	if err := level.UnmarshalText([]byte(cfg.Level)); err != nil {
		level.SetLevel(zap.InfoLevel)
	}
	var encoder zapcore.Encoder
	if cfg.Format == "json" {
		encoder = zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
	} else {
		encoder = zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig())
	}
	// 输出目标：stdout 或文件(文件模式接 lumberjack 轮转: 单文件 200MB,保留 7 份)
	var sink zapcore.WriteSyncer = os.Stdout
	if cfg.Output != "" && cfg.Output != "stdout" {
		sink = zapcore.AddSync(&golumberjack.Logger{
			Filename:   cfg.Output,
			MaxSize:    200,
			MaxBackups: 7,
			Compress:   true,
		})
	}
	return zap.New(zapcore.NewCore(encoder, sink, level))
}

// logFatal 打印错误并退出（zap 初始化前的兜底）
func logFatal(msg string, err error) {
	fmt.Fprintf(os.Stderr, "FATAL %s: %v\n", msg, err)
	os.Exit(1)
}

// proxyScheme 返回代理服务对外 scheme:TLS 启用 → https,否则 http(管理后台服务恒为 http)
func proxyScheme(tlsEnabled bool) string {
	if tlsEnabled {
		return "https"
	}
	return "http"
}

// proxyPort 从代理监听地址(形如 :8080 / 0.0.0.0:8080)解析对外端口;解析失败返回 0(视为未配置)
func proxyPort(proxyAddr string) int {
	_, port, err := net.SplitHostPort(proxyAddr)
	if err != nil {
		return 0
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		return 0
	}
	return p
}
