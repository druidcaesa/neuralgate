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

package oss

import (
	"errors"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// newOrphanedStorage 构造「密文由旧密钥写入、读取时密钥已轮换」的真实孤儿行场景。
// 返回的 reader 用不同 encrypt_key 读取同一份数据。
func newOrphanedStorage(t *testing.T) (writer, reader *SQLStorage) {
	t.Helper()
	writer = newTestSQLStorage(t)
	now := time.Now()
	if err := writer.SaveModelConfig(&plugin.ModelConfig{
		ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: "https://x", APIKey: "sk-secret", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveModelConfig: %v", err)
	}
	reader = &SQLStorage{db: writer.db, encryptKey: "rotated-key"}
	return writer, reader
}

// TestScanDegradesOnDecryptFailure 单行解密失败时列表仍应列出该行并置标志,不得整表失败
func TestScanDegradesOnDecryptFailure(t *testing.T) {
	_, reader := newOrphanedStorage(t)

	cfgs, total, err := reader.ListModelConfigs(1, 10)
	if err != nil {
		t.Fatalf("ListModelConfigs must degrade, got err: %v", err)
	}
	if total != 1 || len(cfgs) != 1 {
		t.Fatalf("ListModelConfigs = %d/%d, want 1/1", len(cfgs), total)
	}
	if !cfgs[0].APIKeyUnreadable {
		t.Error("APIKeyUnreadable = false, want true")
	}
	if cfgs[0].APIKey != "" {
		t.Error("APIKey must be empty when undecryptable, got non-empty")
	}
}

// TestGetByIDDegradesOnDecryptFailure 管理面按主键读取应放行,以便编辑页重填
func TestGetByIDDegradesOnDecryptFailure(t *testing.T) {
	_, reader := newOrphanedStorage(t)

	got, err := reader.GetModelConfigByID("m1")
	if err != nil {
		t.Fatalf("GetModelConfigByID must degrade, got err: %v", err)
	}
	if !got.APIKeyUnreadable || got.ModelName != "gpt-4" {
		t.Fatalf("got %+v, want APIKeyUnreadable=true ModelName=gpt-4", got)
	}
	// 管理面是唯一会把 APIKey 交给 UI/出网的路径,降级时不得把密文原样带回
	if got.APIKey != "" {
		t.Error("APIKey must be empty when undecryptable, got non-empty")
	}
}

// TestGetModelConfigFailsClosedOnDecryptFailure 数据面按名读取必须拒绝,不得返回空密钥
func TestGetModelConfigFailsClosedOnDecryptFailure(t *testing.T) {
	_, reader := newOrphanedStorage(t)

	if _, err := reader.GetModelConfig("gpt-4"); !errors.Is(err, ErrAPIKeyUnreadable) {
		t.Fatalf("GetModelConfig err = %v, want ErrAPIKeyUnreadable", err)
	}
}

// TestHealthyRowUnaffectedByDegrade 复现生产库真实处境:旧密钥写入的孤儿行与轮换后新建的正常行共存,
// 用当前密钥读取时只应降级孤儿行,正常行密钥完整可读
func TestHealthyRowUnaffectedByDegrade(t *testing.T) {
	legacy := newTestSQLStorage(t) // 密钥轮换前的写入器
	now := time.Now()
	if err := legacy.SaveModelConfig(&plugin.ModelConfig{
		ID: "orphan", ModelName: "orphan-model", Provider: "openai", ProviderModel: "p",
		BaseURL: "https://x", APIKey: "sk-old", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveModelConfig(orphan): %v", err)
	}

	// 同一份库,密钥轮换后的写入器:新建行用当前密钥
	current := &SQLStorage{db: legacy.db, encryptKey: "rotated-key"}
	if err := current.SaveModelConfig(&plugin.ModelConfig{
		ID: "ok", ModelName: "ok-model", Provider: "openai", ProviderModel: "p",
		BaseURL: "https://x", APIKey: "sk-good", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveModelConfig(ok): %v", err)
	}

	got, err := current.GetModelConfig("ok-model")
	if err != nil || got.APIKey != "sk-good" || got.APIKeyUnreadable {
		t.Fatalf("healthy row = %+v, %v; want key intact", got, err)
	}

	cfgs, total, err := current.ListModelConfigs(1, 10)
	if err != nil {
		t.Fatalf("ListModelConfigs must degrade, got err: %v", err)
	}
	if total != 2 || len(cfgs) != 2 {
		t.Fatalf("ListModelConfigs = %d/%d, want 2/2", len(cfgs), total)
	}
	byID := make(map[string]*plugin.ModelConfig, len(cfgs))
	for _, c := range cfgs {
		byID[c.ID] = c
	}
	orphan, ok := byID["orphan"]
	if !ok {
		t.Fatal("orphan row missing from list")
	}
	if !orphan.APIKeyUnreadable || orphan.APIKey != "" {
		t.Errorf("orphan row = %+v, want APIKeyUnreadable=true and empty key", orphan)
	}
	healthy, ok := byID["ok"]
	if !ok {
		t.Fatal("healthy row missing from list")
	}
	if healthy.APIKeyUnreadable || healthy.APIKey != "sk-good" {
		t.Errorf("healthy row = %+v, want APIKeyUnreadable=false and key intact", healthy)
	}
}

// TestListUpstreamsDegradesOnDecryptFailure 上游列表对孤儿行同样降级不报错
func TestListUpstreamsDegradesOnDecryptFailure(t *testing.T) {
	writer := newTestSQLStorage(t)
	now := time.Now()
	if err := writer.SaveUpstream(&plugin.Upstream{
		ID: "u1", ModelConfigID: "m1", BaseURL: "https://x",
		APIKey: "sk-up", Weight: 1, Enabled: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveUpstream: %v", err)
	}
	reader := &SQLStorage{db: writer.db, encryptKey: "rotated-key"}

	ups, err := reader.ListUpstreams("m1")
	if err != nil {
		t.Fatalf("ListUpstreams must degrade, got err: %v", err)
	}
	if len(ups) != 1 || !ups[0].APIKeyUnreadable || ups[0].APIKey != "" {
		t.Fatalf("ups = %+v, want one row with APIKeyUnreadable=true and empty key", ups)
	}
}
