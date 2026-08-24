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
	"syscall"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
	"github.com/druidcaesa/neuralgate/pkg/admin"
	"github.com/druidcaesa/neuralgate/pkg/config"
	"github.com/druidcaesa/neuralgate/pkg/core"
	"github.com/druidcaesa/neuralgate/pkg/license"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
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
	if err := storage.Init(map[string]interface{}{
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
	if err := rateLimiter.Init(map[string]interface{}{
		"default_rps": cfg.RateLimit.DefaultRPS,
		"default_tpm": cfg.RateLimit.DefaultTPM,
	}); err != nil {
		logger.Fatal("限流初始化失败", zap.Error(err))
	}
	logger.Info("插件工厂初始化完成", zap.String("storage_driver", cfg.Storage.Driver))

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

	// 6. 初始化代理内核
	pipeline := core.NewPipeline(storage, rateLimiter, auditor, registry)
	proxyCore := core.NewProxyCore(pipeline, registry)
	ipf := core.NewIPFilter(cfg.IPFilter.Mode, cfg.IPFilter.Whitelist, cfg.IPFilter.Blacklist)
	acceptor := core.NewAcceptor(proxyCore.Handler(), ipf)

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
			logger.Warn("未检测到授权文件，以开源模式运行",
				zap.String("path", cfg.License.FilePath), zap.Error(err))
		} else if ok, verr := validator.Validate(info); !ok {
			logger.Warn("授权无效或已过期，降级为开源模式运行", zap.Error(verr))
			licenseOverview.Status = licenseStatus(verr)
			licenseOverview.Message = verr.Error()
		} else {
			effectiveEdition = "enterprise"
			gate = validator
			licenseOverview.Status = "valid"
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

	// 8. 启动双服务（并发）
	tlsHandler := core.NewTLSHandler(cfg.TLS.Enabled, cfg.TLS.CertFile, cfg.TLS.KeyFile, cfg.TLS.MinVersion)
	tlsConf, err := tlsHandler.TLSConfig()
	if err != nil {
		logger.Fatal("TLS 配置加载失败", zap.Error(err))
	}
	proxyServer := &http.Server{
		Addr:           cfg.Server.ProxyAddr,
		Handler:        acceptor.Handler(),
		ReadTimeout:    cfg.Server.ReadTimeout,
		WriteTimeout:   cfg.Server.WriteTimeout,
		IdleTimeout:    cfg.Server.IdleTimeout,
		MaxHeaderBytes: cfg.Server.MaxHeaderBytes,
		TLSConfig:      tlsConf, // nil 时等同普通 HTTP
		// ConnState 回调：追踪连接状态（当前为空实现）
		ConnState: func(c net.Conn, state http.ConnState) {},
	}
	adminHTTPServer := &http.Server{
		Addr:    cfg.Server.AdminAddr,
		Handler: adminServer.Router(),
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

	// 10. Shutdown
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
	if err := auditor.Shutdown(); err != nil {
		logger.Warn("审计管道关闭异常", zap.Error(err))
	} else {
		logger.Info("审计管道已关闭")
	}
	if err := storage.Close(); err != nil {
		logger.Warn("存储关闭异常", zap.Error(err))
	} else {
		logger.Info("存储已关闭")
	}
	logger.Info("NeuralGate 已退出")
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
	return zap.New(zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), level))
}

// logFatal 打印错误并退出（zap 初始化前的兜底）
func logFatal(msg string, err error) {
	fmt.Fprintf(os.Stderr, "FATAL %s: %v\n", msg, err)
	os.Exit(1)
}
