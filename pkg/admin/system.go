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
	"time"

	"github.com/druidcaesa/neuralgate/pkg/core"
	"github.com/gin-gonic/gin"
)

// systemInfo GET /api/system:版本、DB 状态、审计与限流状态、授权概览
func (s *AdminServer) systemInfo(c *gin.Context) {
	uptime := time.Since(s.startedAt).Round(time.Second).String()
	dbStatus := "ok"
	if err := s.storage.Ping(); err != nil {
		dbStatus = "error: " + err.Error()
	}
	ov := s.licenseOverview()
	licenseView := gin.H{"status": ov.Status}
	if ov.Info != nil {
		licenseView["customer"] = ov.Info.CustomerName
		licenseView["expires_at"] = ov.Info.ExpiresAt.Format(time.RFC3339)
		licenseView["features_count"] = len(ov.Info.Features)
	}
	OK(c, gin.H{
		"version":             core.Version,
		"build_time":          core.BuildTime,
		"git_commit":          core.GitCommit,
		"edition":             s.edition,
		"uptime":              uptime,
		"db_status":           dbStatus,
		"audit_queue_status":  gin.H{"status": "ok"},
		"rate_limiter_status": gin.H{"status": "ok"},
		"license":             licenseView,
		"tamper":              gin.H{"unresolved_count": s.unresolvedTamperCount()},
	})
}
