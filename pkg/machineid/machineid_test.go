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

package machineid

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFingerprintDeterministicAndFormat(t *testing.T) {
	a, err := Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	b, _ := Fingerprint()
	if a != b {
		t.Fatalf("指纹应稳定: %q != %q", a, b)
	}
	if len(a) != 32 {
		t.Fatalf("指纹应为 32 位, 得 %d (%q)", len(a), a)
	}
	if strings.ToLower(a) != a {
		t.Fatalf("指纹应为小写 hex: %q", a)
	}
}

func TestRawMachineIDFallbackPersists(t *testing.T) {
	orig := machineIDSources
	machineIDSources = []string{filepath.Join(t.TempDir(), "no-such-machine-id")}
	defer func() { machineIDSources = orig }()

	fb := filepath.Join(t.TempDir(), "sub", "machine")
	id1, err := rawMachineID(fb)
	if err != nil {
		t.Fatalf("rawMachineID: %v", err)
	}
	if id1 == "" {
		t.Fatal("回退应生成非空 ID")
	}
	if _, err := os.Stat(fb); err != nil {
		t.Fatalf("回退应持久化到文件: %v", err)
	}
	id2, _ := rawMachineID(fb)
	if id1 != id2 {
		t.Fatalf("回退 ID 应复用: %q != %q", id1, id2)
	}
}
