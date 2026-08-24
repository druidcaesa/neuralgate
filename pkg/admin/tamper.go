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
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// unresolvedTamperCount 未处置篡改告警数（存储异常时返回 0 并忽略错误）
func (s *AdminServer) unresolvedTamperCount() int64 {
	no := false
	_, total, err := s.storage.ListTamperAlerts(&no, 1, 1)
	if err != nil {
		return 0
	}
	return total
}

// listTamperAlerts GET /api/tamper-alerts?resolved=true|false&page=&size=
func (s *AdminServer) listTamperAlerts(c *gin.Context) {
	var resolvedPtr *bool
	switch c.Query("resolved") {
	case "true":
		v := true
		resolvedPtr = &v
	case "false":
		v := false
		resolvedPtr = &v
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	alerts, total, err := s.storage.ListTamperAlerts(resolvedPtr, page, size)
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	OK(c, gin.H{"items": alerts, "total": total, "page": page, "size": size})
}

// resolveTamperAlert PATCH /api/tamper-alerts/:id body {"resolved":true}
func (s *AdminServer) resolveTamperAlert(c *gin.Context) {
	var req struct {
		Resolved *bool `json:"resolved" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	if err := s.storage.SetTamperAlertResolved(c.Param("id"), *req.Resolved); err != nil {
		Error(c, http.StatusNotFound, 404, err.Error())
		return
	}
	OK(c, gin.H{"resolved": *req.Resolved})
}
