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

import "testing"

// proxyScheme/proxyPort 单测:推导公开 scheme、从代理监听地址解析端口(解析失败=未配置 0)
func TestProxyMetaHelpers(t *testing.T) {
	cases := []struct {
		name  string
		tls   bool
		addr  string
		wantS string
		wantP int
	}{
		{"plain http", false, ":8080", "http", 8080},
		{"tls https", true, ":8443", "https", 8443},
		{"hosted addr", false, "0.0.0.0:9090", "http", 9090},
		{"bare port no colon", false, "8080", "http", 0},
		{"empty addr", false, "", "http", 0},
		{"non numeric port", false, ":abc", "http", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := proxyScheme(tc.tls); got != tc.wantS {
				t.Fatalf("proxyScheme(%v) = %q; want %q", tc.tls, got, tc.wantS)
			}
			if got := proxyPort(tc.addr); got != tc.wantP {
				t.Fatalf("proxyPort(%q) = %d; want %d", tc.addr, got, tc.wantP)
			}
		})
	}
}
