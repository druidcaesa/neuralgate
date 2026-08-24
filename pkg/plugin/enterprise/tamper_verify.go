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
	"strings"
	"sync"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"go.uber.org/zap"
)

const retentionTickInterval = time.Hour // 留存清理节奏

// Tasks 防篡改后台任务组：定期哈希校验(Verifier) + 留存清理(Retainer)。
// Start 前不运行；未 Start 时 Stop 为安全空操作
type Tasks struct {
	storage plugin.StoragePlugin
	algo    string
	logger  *zap.Logger

	verifyInterval time.Duration
	verifyBatch    int
	retention      time.Duration // <=0 表示不启用清理

	mu      sync.Mutex
	started bool
	stopCh  chan struct{}
	doneCh  chan struct{}
}

// NewTasks 创建任务组（verifyInterval 为校验节奏，verifyBatchSize 为扫描页大小，
// retention 为日志保留时长）
func NewTasks(storage plugin.StoragePlugin, algo string, verifyInterval time.Duration,
	verifyBatchSize int, retention time.Duration, logger *zap.Logger) *Tasks {
	return &Tasks{
		storage:        storage,
		algo:           algo,
		logger:         logger,
		verifyInterval: verifyInterval,
		verifyBatch:    verifyBatchSize,
		retention:      retention,
		stopCh:         make(chan struct{}),
		doneCh:         make(chan struct{}),
	}
}

// Start 启动校验与清理循环；重复调用无效果
func (t *Tasks) Start() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.started {
		return
	}
	t.started = true

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t.cycleLoop(t.verifyInterval, t.verifyOnce)
	}()
	if t.retention > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t.cycleLoop(retentionTickInterval, t.retainOnce)
		}()
	}
	go func() {
		wg.Wait()
		close(t.doneCh)
	}()
}

// Stop 停止全部循环并等待退出；未 Start 过为安全空操作
func (t *Tasks) Stop() {
	t.mu.Lock()
	started := t.started
	t.mu.Unlock()
	if !started {
		return
	}
	select {
	case <-t.stopCh: // 已停止
	default:
		close(t.stopCh)
	}
	<-t.doneCh
}

// cycleLoop 启动即执行一轮，此后按 interval 周期触发直至停止
func (t *Tasks) cycleLoop(interval time.Duration, fn func()) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	fn()
	for {
		select {
		case <-t.stopCh:
			return
		case <-ticker.C:
			fn()
		}
	}
}

// verifyOnce 全库滚动扫描一轮：按 CreatedAt DESC 分页重算指纹比对；
// 存储指纹为空的历史记录跳过不误报；不一致的收集为告警一次性 upsert 去重
func (t *Tasks) verifyOnce() {
	var pending []*plugin.TamperAlert
	for page := 1; ; page++ {
		logs, _, err := t.storage.QueryAuditLogs(plugin.AuditLogFilter{}, page, t.verifyBatch)
		if err != nil {
			t.logger.Warn("哈希校验拉取失败", zap.Error(err))
			return
		}
		for _, l := range logs {
			if l.SHA256Fingerprint == "" || strings.EqualFold(l.SHA256Fingerprint, Fingerprint(t.algo, l)) {
				continue
			}
			pending = append(pending, &plugin.TamperAlert{
				AuditLogID: l.ID,
				Reason:     "内容与存证指纹不一致",
			})
		}
		if len(logs) < t.verifyBatch {
			break
		}
	}
	if len(pending) > 0 {
		if err := t.storage.SaveTamperAlerts(pending); err != nil {
			t.logger.Warn("篡改告警写入失败", zap.Error(err))
			return
		}
		t.logger.Warn("检测到审计日志疑似被篡改", zap.Int("count", len(pending)))
	}
}

// retainOnce 清理超过留存期的审计日志
func (t *Tasks) retainOnce() {
	n, err := t.storage.DeleteAuditLogsBefore(time.Now().Add(-t.retention))
	if err != nil {
		t.logger.Warn("留存清理失败", zap.Error(err))
		return
	}
	if n > 0 {
		t.logger.Info("留存清理完成", zap.Int64("deleted", n))
	}
}
