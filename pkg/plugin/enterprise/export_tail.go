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

//go:build enterprise

package enterprise

import (
	"errors"
	"sync"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"go.uber.org/zap"
)

const (
	exportBufferLimit      = 10000            // 待重试缓冲上限
	pullPageSize           = 1000             // 游标查询单页上限
	baseBackoff            = time.Second      // 退避基准
	maxBackoff             = 30 * time.Second // 退避封顶
	cursorOverlap          = time.Second      // 游标向前重叠窗口(配合 seen 去重)
	defaultExportBatchSize = 50
	defaultFlushInterval   = 10 * time.Second
)

// backoffDelay 第 failures 次连续失败后的重试间隔：1s 起倍增，30s 封顶
func backoffDelay(failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	d := baseBackoff
	for i := 1; i < failures; i++ {
		d *= 2
		if d >= maxBackoff {
			return maxBackoff
		}
	}
	return d
}

// TailExporter 存储尾随外推器：时间游标拉取新增审计日志批量推送，
// 失败整批入有界缓冲按指数退避补投。实现 plugin.LogExporter：
// Init 配置并启动后台循环，Close 收尾；Export/BatchExport 为兼容接口直入缓冲。
// target/cursor/seen/buffer 等状态均由 mu 保护；target.Send 必须在锁外调用。
type TailExporter struct {
	storage plugin.StoragePlugin
	target  ExportTarget
	logger  *zap.Logger

	batchSize     int
	flushInterval time.Duration
	bufferLimit   int

	mu          sync.Mutex
	cursor      time.Time            // 已拉取的最大 CreatedAt
	seen        map[string]time.Time // 已拉取 ID -> CreatedAt，重叠窗口去重
	buffer      []*plugin.AuditLog   // FIFO 待推送缓冲
	failures    int                  // 连续推送失败次数
	nextAttempt time.Time            // 退避到期时刻
	stopCh      chan struct{}
	doneCh      chan struct{}
	stopped     bool
}

// NewTailExporter 创建未初始化的外推器；须经 Init 后才开始工作
func NewTailExporter(storage plugin.StoragePlugin) *TailExporter {
	return &TailExporter{
		storage:       storage,
		logger:        zap.NewNop(),
		batchSize:     defaultExportBatchSize,
		flushInterval: defaultFlushInterval,
		bufferLimit:   exportBufferLimit,
		seen:          make(map[string]time.Time),
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
}

// Init 解析配置、构造目标并启动后台循环；cursor 从当前时刻起（只外推运行期新增）
func (e *TailExporter) Init(config map[string]interface{}) error {
	e.mu.Lock()
	if e.target != nil || e.stopped {
		e.mu.Unlock()
		return errors.New("外推器已初始化或已关闭")
	}
	e.mu.Unlock()

	endpoint, _ := config["endpoint"].(string)
	if endpoint == "" {
		return errors.New("外推 endpoint 不能为空")
	}
	exportType, _ := config["type"].(string)
	apiKey, _ := config["api_key"].(string)
	target, err := NewExportTarget(exportType, endpoint, apiKey)
	if err != nil {
		return err
	}
	if v, ok := config["batch_size"].(int); ok && v > 0 {
		e.batchSize = v
	}
	if v, ok := config["flush_interval"].(time.Duration); ok && v > 0 {
		e.flushInterval = v
	}
	if l, ok := config["logger"].(*zap.Logger); ok && l != nil {
		e.logger = l
	}

	e.mu.Lock()
	e.target = target
	e.cursor = time.Now()
	e.seen = make(map[string]time.Time)
	e.stopCh = make(chan struct{})
	e.doneCh = make(chan struct{})
	e.mu.Unlock()

	go e.run()
	e.logger.Info("审计外推器已启动",
		zap.String("type", exportType), zap.Duration("interval", e.flushInterval))
	return nil
}

func (e *TailExporter) run() {
	defer close(e.doneCh)
	ticker := time.NewTicker(e.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-e.stopCh:
			return
		case <-ticker.C:
			e.drain()
		}
	}
}

// drain 一轮「拉取入缓冲 → 按退避节奏推送一批」
func (e *TailExporter) drain() {
	e.pullNew()
	e.flushOnce()
}

// pullNew 以游标前移重叠窗查询新增日志；存储固定 CreatedAt DESC 序故倒序遍历得升序；
// seen 去重后入缓冲并推进游标，最后清理重叠窗外的 seen 记录防无限增长
func (e *TailExporter) pullNew() {
	e.mu.Lock()
	defer e.mu.Unlock()
	from := e.cursor.Add(-cursorOverlap)
	newest := e.cursor
	for page := 1; ; page++ {
		logs, _, err := e.storage.QueryAuditLogs(plugin.AuditLogFilter{StartTime: &from}, page, pullPageSize)
		if err != nil {
			e.logger.Warn("外推拉取审计日志失败", zap.Error(err))
			return
		}
		for i := len(logs) - 1; i >= 0; i-- {
			l := logs[i]
			if _, dup := e.seen[l.ID]; dup {
				continue
			}
			e.seen[l.ID] = l.CreatedAt
			e.enqueueLocked(l)
			if l.CreatedAt.After(newest) {
				newest = l.CreatedAt
			}
		}
		if len(logs) < pullPageSize {
			break
		}
	}
	e.cursor = newest
	for id, ts := range e.seen {
		if ts.Before(e.cursor.Add(-cursorOverlap)) {
			delete(e.seen, id)
		}
	}
}

// enqueueLocked 入缓冲，满则丢最旧并告警（调用方须持锁）
func (e *TailExporter) enqueueLocked(log *plugin.AuditLog) {
	if len(e.buffer) >= e.bufferLimit {
		dropped := e.buffer[0]
		e.buffer = e.buffer[1:]
		e.logger.Warn("外推缓冲已满，丢弃最旧日志",
			zap.String("dropped_request_id", dropped.RequestID), zap.Int("limit", e.bufferLimit))
	}
	e.buffer = append(e.buffer, log)
}

// flushOnce 推送一批；成功则连续清空至缓冲空或再次失败，失败设置退避
func (e *TailExporter) flushOnce() {
	for {
		e.mu.Lock()
		if len(e.buffer) == 0 || time.Now().Before(e.nextAttempt) {
			e.mu.Unlock()
			return
		}
		n := e.batchSize
		if n > len(e.buffer) {
			n = len(e.buffer)
		}
		batch := make([]*plugin.AuditLog, n)
		copy(batch, e.buffer[:n])
		target := e.target
		e.mu.Unlock()

		err := target.Send(batch)
		e.mu.Lock()
		if err != nil {
			e.failures++
			delay := backoffDelay(e.failures)
			e.nextAttempt = time.Now().Add(delay)
			e.logger.Warn("外推推送失败，进入退避重试",
				zap.Error(err), zap.Int("buffered", len(e.buffer)), zap.Duration("delay", delay))
			e.mu.Unlock()
			return
		}
		e.buffer = e.buffer[n:]
		e.failures = 0
		e.mu.Unlock()
	}
}

// Close 停止循环 → 最终一轮拉取（兜住 auditor.Shutdown 落库的尾部日志）→
// 无视退避尽力清空缓冲 → 关闭目标。未 Init 过为安全空操作。
func (e *TailExporter) Close() error {
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return nil
	}
	e.stopped = true
	started := e.target != nil
	close(e.stopCh)
	interval := e.flushInterval
	e.mu.Unlock()

	if !started {
		return nil
	}
	select {
	case <-e.doneCh:
	case <-time.After(3 * interval):
	}
	e.pullNew()
	e.flushAll()
	e.mu.Lock()
	target := e.target
	e.mu.Unlock()
	return target.Close()
}

// flushAll 无视退避连续推送直到缓冲清空或失败
func (e *TailExporter) flushAll() {
	for {
		e.mu.Lock()
		if len(e.buffer) == 0 {
			e.mu.Unlock()
			return
		}
		n := e.batchSize
		if n > len(e.buffer) {
			n = len(e.buffer)
		}
		batch := make([]*plugin.AuditLog, n)
		copy(batch, e.buffer[:n])
		target := e.target
		e.mu.Unlock()

		if err := target.Send(batch); err != nil {
			e.logger.Warn("关闭前外推未完成", zap.Error(err))
			return
		}
		e.mu.Lock()
		e.buffer = e.buffer[n:]
		e.mu.Unlock()
	}
}

// ===== plugin.LogExporter 兼容方法 =====

// Export 单条直入缓冲（主路径为存储尾随拉取）
func (e *TailExporter) Export(log *plugin.AuditLog) error {
	if log == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.enqueueLocked(log)
	return nil
}

// BatchExport 批量直入缓冲
func (e *TailExporter) BatchExport(logs []*plugin.AuditLog) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, l := range logs {
		if l != nil {
			e.enqueueLocked(l)
		}
	}
	return nil
}

// TestConnection 透传目标的连通性检查
func (e *TailExporter) TestConnection() error {
	e.mu.Lock()
	target := e.target
	e.mu.Unlock()
	if target == nil {
		return errors.New("外推器尚未初始化")
	}
	return target.TestConnection()
}
