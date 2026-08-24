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
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
	"go.uber.org/zap"
)

// newTestTasks 构造不经 Start 的任务组（直接调 verifyOnce/retainOnce）
func newTestTasks(storage plugin.StoragePlugin) *Tasks {
	return NewTasks(storage, "sha256", time.Hour, 100, 24*time.Hour, zap.NewNop())
}

// storeVerified 写入一条指纹正确的日志
func storeVerified(t *testing.T, storage plugin.StoragePlugin, id string) *plugin.AuditLog {
	t.Helper()
	log := &plugin.AuditLog{ID: id, RequestID: id, ModelName: "m", ResponseStatus: 200, CreatedAt: time.Now()}
	log.SHA256Fingerprint = Fingerprint("sha256", log)
	if err := storage.SaveAuditLog(log); err != nil {
		t.Fatal(err)
	}
	return log
}

func TestVerifyOnceDetectsTamperingAndDedups(t *testing.T) {
	storage := oss.NewMemStorage()
	ok := storeVerified(t, storage, "ok-1")

	tampered := storeVerified(t, storage, "bad-1")
	tampered.RequestBody = `{"model":"forged"}` // 落库后内容被改，与存储指纹失配
	if err := storage.SaveAuditLog(tampered); err != nil {
		t.Fatal(err)
	}
	_ = ok

	tasks := newTestTasks(storage)
	tasks.verifyOnce()

	alerts, total, err := storage.ListTamperAlerts(nil, 1, 10)
	if err != nil || total != 1 || len(alerts) != 1 {
		t.Fatalf("应恰好产生 1 条告警: total=%d err=%v", total, err)
	}
	if alerts[0].AuditLogID != "bad-1" {
		t.Errorf("告警应指向被篡改日志, got %s", alerts[0].AuditLogID)
	}

	// 再次扫描：同一日志去重不重复插入，仅更新检查时间
	before := alerts[0].LastCheckedAt
	time.Sleep(2 * time.Millisecond) // 保证时间戳可区分
	tasks.verifyOnce()
	alerts, total, _ = storage.ListTamperAlerts(nil, 1, 10)
	if total != 1 {
		t.Fatalf("重复扫描不应新增告警, got %d", total)
	}
	if !alerts[0].LastCheckedAt.After(before) {
		t.Error("重复扫描应更新 LastCheckedAt")
	}
}

func TestVerifyOnceSkipsEmptyFingerprint(t *testing.T) {
	storage := oss.NewMemStorage()
	// 历史未存证数据：指纹为空，不参与比对也不误报
	if err := storage.SaveAuditLog(&plugin.AuditLog{ID: "legacy", RequestID: "legacy", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	tasks := newTestTasks(storage)
	tasks.verifyOnce()
	if _, total, _ := storage.ListTamperAlerts(nil, 1, 10); total != 0 {
		t.Errorf("空指纹记录不应产生告警, got %d", total)
	}
}

func TestRetainOnceDeletesExpiredOnly(t *testing.T) {
	storage := oss.NewMemStorage()
	old := &plugin.AuditLog{ID: "old", RequestID: "old", CreatedAt: time.Now().Add(-72 * time.Hour)}
	fresh := &plugin.AuditLog{ID: "new", RequestID: "new", CreatedAt: time.Now()}
	for _, l := range []*plugin.AuditLog{old, fresh} {
		l.SHA256Fingerprint = Fingerprint("sha256", l)
		if err := storage.SaveAuditLog(l); err != nil {
			t.Fatal(err)
		}
	}

	tasks := newTestTasks(storage)
	tasks.retainOnce()

	if _, total, _ := storage.QueryAuditLogs(plugin.AuditLogFilter{RequestID: "old"}, 1, 10); total != 0 {
		t.Error("超期日志应已删除")
	}
	if _, total, _ := storage.QueryAuditLogs(plugin.AuditLogFilter{RequestID: "new"}, 1, 10); total != 1 {
		t.Error("未到期日志应保留")
	}
}
