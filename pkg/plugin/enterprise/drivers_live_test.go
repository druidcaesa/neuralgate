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

package enterprise_test

import (
	"os"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

// 真库门控: 环境变量提供 DSN 才执行(NG_DM_DSN / NG_KINGBASE_DSN)。
// 覆盖 建表幂等(重复 Init)+UPSERT 往返 两个最小闭环,其余 CRUD 与
// mysql/sqlite 共享同一实现路径,由单测矩阵保障
func TestXCDatabaseLive(t *testing.T) {
	cases := []struct {
		name string
		env  string
	}{
		{"dameng", "NG_DM_DSN"},
		{"kingbase", "NG_KINGBASE_DSN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dsn := os.Getenv(tc.env)
			if dsn == "" {
				t.Skipf("%s 未设置,跳过 %s 真库验证", tc.env, tc.name)
			}
			driver := map[string]string{"dameng": "dm", "kingbase": "kingbase"}[tc.name]
			for round := 0; round < 2; round++ { // 两轮验证建表/种子幂等
				s := oss.NewSQLStorage()
				if err := s.Init(map[string]interface{}{
					"driver": driver, "dsn": dsn,
					"encrypt_key": "xc-live-test-key"}); err != nil {
					t.Fatalf("第 %d 轮 Init: %v", round+1, err)
				}
				if round == 1 {
					srv := &plugin.MCPServer{
						Name: "xc-live", Endpoint: "http://127.0.0.1:1/mcp",
						Enabled: true, CreatedAt: time.Now(), UpdatedAt: time.Now(),
					}
					if err := s.SaveMCPServer(srv); err != nil {
						t.Fatalf("SaveMCPServer: %v", err)
					}
					got, err := s.GetMCPServer(srv.ID)
					if err != nil || got.Name != "xc-live" {
						t.Fatalf("往返不符: %v %+v", err, got)
					}
				}
				s.Close()
			}
		})
	}
}
