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
	"regexp"
	"strconv"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// privacyRuleRequest 规则创建/更新请求体
type privacyRuleRequest struct {
	RuleType    string `json:"rule_type" binding:"required,oneof=pii injection"`
	Name        string `json:"name" binding:"required,min=1,max=64"`
	Pattern     string `json:"pattern" binding:"required,max=512"`
	Replacement string `json:"replacement" binding:"max=128"`
	Scope       string `json:"scope" binding:"required,oneof=request response both"`
}

// validPattern 正则可编译校验：入库前即时反馈，避免规则加载时被引擎静默跳过
func validPattern(pattern string) bool {
	_, err := regexp.Compile(pattern)
	return err == nil
}

// createPrivacyRule POST /api/privacy-rules
func (s *AdminServer) createPrivacyRule(c *gin.Context) {
	var req privacyRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	if req.RuleType == plugin.PrivacyRuleTypeInjection {
		// 注入检测恒 request 作用域，replacement 无意义
		req.Scope = plugin.PrivacyScopeRequest
		req.Replacement = ""
	}
	if !validPattern(req.Pattern) {
		Error(c, http.StatusBadRequest, 400, "pattern 不是合法正则")
		return
	}
	now := time.Now()
	rule := &plugin.PrivacyRule{
		ID: uuid.NewString(), RuleType: req.RuleType, Name: req.Name,
		Pattern: req.Pattern, Replacement: req.Replacement, Scope: req.Scope,
		Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.storage.SavePrivacyRule(rule); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to save privacy rule")
		return
	}
	OK(c, gin.H{"id": rule.ID})
}

// listPrivacyRules GET /api/privacy-rules?rule_type=pii|injection
func (s *AdminServer) listPrivacyRules(c *gin.Context) {
	var ruleType *string
	switch c.Query("rule_type") {
	case plugin.PrivacyRuleTypePII:
		v := plugin.PrivacyRuleTypePII
		ruleType = &v
	case plugin.PrivacyRuleTypeInjection:
		v := plugin.PrivacyRuleTypeInjection
		ruleType = &v
	}
	rules, err := s.storage.ListPrivacyRules(ruleType)
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to list privacy rules")
		return
	}
	OK(c, gin.H{"items": rules})
}

// updatePrivacyRule PUT /api/privacy-rules/:id
func (s *AdminServer) updatePrivacyRule(c *gin.Context) {
	id := c.Param("id")
	var req privacyRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	if req.RuleType == plugin.PrivacyRuleTypeInjection {
		req.Scope = plugin.PrivacyScopeRequest
		req.Replacement = ""
	}
	if !validPattern(req.Pattern) {
		Error(c, http.StatusBadRequest, 400, "pattern 不是合法正则")
		return
	}
	existing, err := s.storage.ListPrivacyRules(nil)
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to load privacy rules")
		return
	}
	var rule *plugin.PrivacyRule
	for _, r := range existing {
		if r.ID == id {
			rule = r
			break
		}
	}
	if rule == nil {
		Error(c, http.StatusNotFound, 404, "privacy rule not found")
		return
	}
	rule.RuleType = req.RuleType
	rule.Name = req.Name
	rule.Pattern = req.Pattern
	rule.Replacement = req.Replacement
	rule.Scope = req.Scope
	rule.UpdatedAt = time.Now()
	if err := s.storage.SavePrivacyRule(rule); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to update privacy rule")
		return
	}
	OK(c, gin.H{"id": id})
}

// deletePrivacyRule DELETE /api/privacy-rules/:id
func (s *AdminServer) deletePrivacyRule(c *gin.Context) {
	id := c.Param("id")
	if err := s.storage.DeletePrivacyRule(id); err != nil {
		Error(c, http.StatusNotFound, 404, "privacy rule not found")
		return
	}
	OK(c, gin.H{"id": id, "deleted": true})
}

// privacyWhitelistRequest 白名单创建请求体
type privacyWhitelistRequest struct {
	Pattern string `json:"pattern" binding:"required,max=512"`
	Note    string `json:"note" binding:"max=255"`
	Enabled *bool  `json:"enabled"`
}

// createPrivacyWhitelistEntry POST /api/privacy-whitelist
func (s *AdminServer) createPrivacyWhitelistEntry(c *gin.Context) {
	var req privacyWhitelistRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	if !validPattern(req.Pattern) {
		Error(c, http.StatusBadRequest, 400, "pattern 不是合法正则")
		return
	}
	entry := &plugin.PrivacyWhitelistEntry{
		ID: uuid.NewString(), Pattern: req.Pattern, Note: req.Note,
		Enabled: true, CreatedAt: time.Now(),
	}
	if err := s.storage.SavePrivacyWhitelistEntry(entry); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to save whitelist entry")
		return
	}
	OK(c, gin.H{"id": entry.ID})
}

// listPrivacyWhitelistEntries GET /api/privacy-whitelist
func (s *AdminServer) listPrivacyWhitelistEntries(c *gin.Context) {
	entries, err := s.storage.ListPrivacyWhitelistEntries()
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to list whitelist entries")
		return
	}
	OK(c, gin.H{"items": entries})
}

// deletePrivacyWhitelistEntry DELETE /api/privacy-whitelist/:id
func (s *AdminServer) deletePrivacyWhitelistEntry(c *gin.Context) {
	id := c.Param("id")
	if err := s.storage.DeletePrivacyWhitelistEntry(id); err != nil {
		Error(c, http.StatusNotFound, 404, "whitelist entry not found")
		return
	}
	OK(c, gin.H{"id": id, "deleted": true})
}

// listSecurityEvents GET /api/security-events?page=&size=
func (s *AdminServer) listSecurityEvents(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	events, total, err := s.storage.ListSecurityEvents(page, size)
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to list security events")
		return
	}
	OK(c, gin.H{"items": events, "total": total, "page": page, "size": size})
}
