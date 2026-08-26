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

package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// TestBatchCreateAPIKeys 批量创建:N 个明文唯一、前缀编号、租户归属正确
func TestBatchCreateAPIKeys(t *testing.T) {
	f := newRBACFixture(t, true)
	rec := f.do(f.superTok, http.MethodPost, "/api/api-keys/batch-create",
		`{"name_prefix":"team-a","count":3,"quota":-1}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("批量创建应成功: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			Items []struct {
				ID   string `json:"id"`
				Key  string `json:"key"`
				Name string `json:"name"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	items := resp.Data.Items
	if len(items) != 3 {
		t.Fatalf("应生成 3 个, got %d", len(items))
	}
	seen := map[string]bool{}
	for i, it := range items {
		if !strings.HasPrefix(it.Key, "ng-") || seen[it.Key] {
			t.Errorf("Key 应唯一且 ng- 前缀: %s", it.Key)
		}
		seen[it.Key] = true
		wantName := "team-a-0" + string(rune('1'+i))
		if it.Name != wantName {
			t.Errorf("名称应为 %s, got %s", wantName, it.Name)
		}
	}
}

// TestBatchCreateValidation count 越界 400
func TestBatchCreateValidation(t *testing.T) {
	f := newRBACFixture(t, true)
	for _, body := range []string{
		`{"name_prefix":"x","count":0}`,
		`{"name_prefix":"x","count":101}`,
	} {
		if rec := f.do(f.superTok, http.MethodPost, "/api/api-keys/batch-create", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s 应 400, got %d", body, rec.Code)
		}
	}
}

// TestBatchDeleteAPIKeys 删除计数与缺失列表；租户隔离(跨租户计入 missing)
func TestBatchDeleteAPIKeys(t *testing.T) {
	f := newRBACFixture(t, true)
	rec := f.do(f.superTok, http.MethodPost, "/api/api-keys/batch-create",
		`{"name_prefix":"del","count":3,"quota":-1}`)
	var created struct {
		Data struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	ids := []string{created.Data.Items[0].ID, created.Data.Items[1].ID}

	rec = f.do(f.superTok, http.MethodPost, "/api/api-keys/batch-delete",
		`{"ids":["`+ids[0]+`","`+ids[1]+`","no-such-id"]}`)
	var delResp struct {
		Data struct {
			Deleted int      `json:"deleted"`
			Missing []string `json:"missing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &delResp); err != nil {
		t.Fatal(err)
	}
	if delResp.Data.Deleted != 2 || len(delResp.Data.Missing) != 1 || delResp.Data.Missing[0] != "no-such-id" {
		t.Errorf("删除结果不符: %+v", delResp.Data)
	}
}

// TestBatchCreateScopedTenant 租户内用户批量创建强制归属自身租户
func TestBatchCreateScopedTenant(t *testing.T) {
	f := newRBACFixture(t, true)
	wide := &plugin.Role{Name: "越权角色", TenantID: rbacTestTenantA, Permissions: plugin.AllPermissions}
	_ = f.storage.SaveRole(wide)
	scoped, _ := f.storage.GetAdminUserByID("u-scoped")
	scoped.RoleID = wide.ID
	_ = f.storage.SaveAdminUser(scoped)
	sm := NewSessionManager([]byte("rbac-test-secret"), time.Hour)
	tok, _, _ := sm.Mint(scoped, plugin.AllPermissions, false, time.Now())

	rec := f.do(tok, http.MethodPost, "/api/api-keys/batch-create",
		`{"name_prefix":"scoped","count":2,"quota":-1,"tenant_id":"other-tenant"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("租户用户批量创建应成功: %d %s", rec.Code, rec.Body.String())
	}
	list, _, _ := f.storage.ListAPIKeys(rbacTestTenantA, 1, 10)
	if len(list) != 2 {
		t.Errorf("Key 应归属租户 A(强制覆盖请求中的 other-tenant): got %d", len(list))
	}
}
