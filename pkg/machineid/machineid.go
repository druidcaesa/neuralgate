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

// Package machineid 计算本机稳定指纹,用于授权设备绑定。
package machineid

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// salt 使机器码与本产品绑定,并避免暴露原始 machine-id(非安全边界,真正防伪靠签名)
const salt = "neuralgate-license-v1"

// machineIDSources 稳定机器标识来源(按序取首个非空);包级变量便于测试覆盖
var machineIDSources = []string{"/etc/machine-id", "/var/lib/dbus/machine-id"}

// Fingerprint 返回加盐 SHA256 的前 32 位小写 hex
func Fingerprint() (string, error) {
	raw, err := rawMachineID(defaultFallbackPath())
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(salt + raw))
	return hex.EncodeToString(sum[:])[:32], nil
}

// rawMachineID 依次尝试 machineIDSources,均不可用时回退到持久化随机 UUID
func rawMachineID(fallbackPath string) (string, error) {
	for _, p := range machineIDSources {
		if b, err := os.ReadFile(p); err == nil {
			if s := strings.TrimSpace(string(b)); s != "" {
				return s, nil
			}
		}
	}
	return persistedFallback(fallbackPath)
}

// persistedFallback 读取已存在的回退 ID,不存在则生成随机 UUID 并写入(0600)
func persistedFallback(path string) (string, error) {
	if b, err := os.ReadFile(path); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s, nil
		}
	}
	id := uuid.NewString()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(id), 0o600); err != nil {
		return "", err
	}
	return id, nil
}

// defaultFallbackPath 回退 ID 的持久化位置(用户配置目录不可用时退到临时目录)
func defaultFallbackPath() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "neuralgate", "machine")
}
