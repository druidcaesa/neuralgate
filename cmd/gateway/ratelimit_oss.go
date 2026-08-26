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

//go:build !enterprise

package main

import (
	"github.com/druidcaesa/neuralgate/pkg/config"
	"github.com/druidcaesa/neuralgate/pkg/core"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"go.uber.org/zap"
)

// setupDistributedRateLimit OSS 版无分布式限流，恒返回本地实现且零日志噪音
func setupDistributedRateLimit(_ core.LicenseGate, _ config.Config,
	rateLimiter plugin.RateLimitPlugin, _ *zap.Logger) plugin.RateLimitPlugin {
	return rateLimiter
}
