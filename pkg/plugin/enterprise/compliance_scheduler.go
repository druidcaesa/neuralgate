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

package enterprise

import (
	"errors"
	"sync"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
	"go.uber.org/zap"
)

const (
	reportTickInterval = time.Minute  // 到期扫描节奏：分钟级即可，报表粒度是自然日
	reportCatchUpDays  = 35           // 启动补扫回看天数：覆盖一个完整自然月并留余量
	reportDayFormat    = "2006-01-02" // 到期项 ref 的日期串格式
)

// ReportScheduler 合规报表调度器：分钟级检查到期周期补生成缺失报表，
// 启动时先回扫近期历史弥补停机期间的漏账。生命周期仿后台任务组约定：
// Start 前不运行，未 Start 时 Stop 为安全空操作
type ReportScheduler struct {
	storage plugin.StoragePlugin
	logger  *zap.Logger

	mu      sync.Mutex
	started bool
	stopCh  chan struct{}
	doneCh  chan struct{}
}

// NewReportScheduler 创建合规报表调度器
func NewReportScheduler(storage plugin.StoragePlugin, logger *zap.Logger) *ReportScheduler {
	return &ReportScheduler{
		storage: storage,
		logger:  logger,
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
}

// Start 先同步执行一次历史补扫，再进入分钟级定时循环；重复调用无效果
func (s *ReportScheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return
	}
	s.started = true

	s.catchUpMissing()
	go s.loop()
}

// Stop 停止循环并等待退出；重复调用与未 Start 均为安全空操作。
// 检查并关闭 stopCh 全程持 mu，保证并发 Stop 仅首个调用者执行 close，
// 其余落入已关闭分支只做等待；等 doneCh 在锁外进行——loop 虽不取锁，
// 但 Start 持锁跑补扫，持锁等待会与之互锁。未 Start 时不等待直接返回
func (s *ReportScheduler) Stop() {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return
	}
	select {
	case <-s.stopCh: // 已停止
	default:
		close(s.stopCh)
	}
	s.mu.Unlock()

	<-s.doneCh
}

// loop 定时驱动到期检查直至停止；单轮 panic 不中断循环（后台任务不带崩网关）
func (s *ReportScheduler) loop() {
	defer close(s.doneCh)
	ticker := time.NewTicker(reportTickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			runWithRecover(s.logger, "report-tick", s.tickOnce)
		}
	}
}

// tickOnce 处理此刻到期的周期。与补扫不同：定时路径对空周期也无条件生成——
// 合规留存语义要求昨日即使零流量也有日报可查；已存在的周期判重跳过不重写，
// 避免每分钟反复聚合覆盖
func (s *ReportScheduler) tickOnce() {
	for _, item := range dueReports(time.Now()) {
		ref, err := time.ParseInLocation(reportDayFormat, item[1], time.Local)
		if err != nil {
			s.logger.Warn("到期项 ref 解析失败",
				zap.String("period_type", item[0]), zap.String("ref", item[1]), zap.Error(err))
			continue
		}
		exists, err := s.reportExists(item[0], ref)
		if err != nil {
			s.logger.Warn("合规报表判重失败", zap.String("period_type", item[0]), zap.Error(err))
			continue // 下个 tick 重试
		}
		if exists {
			continue
		}
		if _, err := GenerateComplianceReport(s.storage, s.logger, item[0], ref); err != nil {
			s.logger.Warn("合规报表生成失败", zap.String("period_type", item[0]), zap.Error(err))
		}
	}
}

// catchUpMissing 回扫近 N 天的已完成日：对每日及其所在周/月按业务键判重后补生成。
// 与定时路径不同：零流量的历史周期跳过不生成全零报表——回扫只为有流量的
// 历史周期补档，避免停机空窗刷出大量无意义记录；同一周/月在回看窗口内
// 会命中多次，按周期起点去重只处理一遍
func (s *ReportScheduler) catchUpMissing() {
	now := time.Now()
	type periodKey struct {
		periodType string
		startUnix  int64 // 周期起点 Unix 秒，跨 ref 归并同周/同月
	}
	seen := make(map[periodKey]bool)
	for i := 1; i <= reportCatchUpDays; i++ { // 从昨日回看：当日尚未完结不参与
		ref := now.AddDate(0, 0, -i)
		for _, pt := range []string{plugin.PeriodDay, plugin.PeriodWeek, plugin.PeriodMonth} {
			start, end := BuildRange(pt, ref)
			key := periodKey{pt, start.Unix()}
			if seen[key] {
				continue
			}
			seen[key] = true
			exists, err := s.reportExists(pt, ref)
			if err != nil {
				s.logger.Warn("合规报表判重失败", zap.String("period_type", pt), zap.Time("start", start), zap.Error(err))
				continue
			}
			if exists || !s.periodHasLogs(start, end) {
				continue
			}
			if _, err := GenerateComplianceReport(s.storage, s.logger, pt, ref); err != nil {
				s.logger.Warn("补扫生成合规报表失败", zap.String("period_type", pt), zap.Time("start", start), zap.Error(err))
			}
		}
	}
}

// reportExists 按(周期类型, ref 归一后的周期起点)判重；仅 ErrNotFound 视为缺失，
// 其余错误向上返回交由调用方决定重试策略
func (s *ReportScheduler) reportExists(periodType string, ref time.Time) (bool, error) {
	start, _ := BuildRange(periodType, ref)
	_, err := s.storage.FindComplianceReportByPeriod(periodType, start)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, oss.ErrNotFound):
		return false, nil
	default:
		return false, err
	}
}

// periodHasLogs 轻量探针：单页一条的时间过滤查询判断区间内是否有审计日志，
// 供补扫免于对空周期做全量聚合
func (s *ReportScheduler) periodHasLogs(start, end time.Time) bool {
	_, total, err := s.storage.QueryAuditLogs(plugin.AuditLogFilter{StartTime: &start, EndTime: &end}, 1, 1)
	if err != nil {
		s.logger.Warn("区间审计日志探测失败", zap.Time("start", start), zap.Time("end", end), zap.Error(err))
		return false
	}
	return total > 0
}

// dueReports 纯函数：返回此刻应存在而可能缺失的 [{period_type, ref 日期串}...]。
// 三类触发时刻错峰（日报 00:05 / 周报周一 00:10 / 月报月初 00:15）避免整点集中聚合；
// ref 取目标周期内的代表日（昨日 / 上周一即本周一减 7 天 / 上月 1 日），
// 由 GenerateComplianceReport 经 BuildRange 归一到周期起点
func dueReports(now time.Time) [][2]string {
	y, m, d := now.Date()
	at := func(hour, min int) time.Time {
		return time.Date(y, m, d, hour, min, 0, 0, now.Location())
	}
	var due [][2]string
	if !now.Before(at(0, 5)) {
		due = append(due, [2]string{plugin.PeriodDay, now.AddDate(0, 0, -1).Format(reportDayFormat)})
	}
	if now.Weekday() == time.Monday && !now.Before(at(0, 10)) {
		due = append(due, [2]string{plugin.PeriodWeek, now.AddDate(0, 0, -7).Format(reportDayFormat)})
	}
	if d == 1 && !now.Before(at(0, 15)) {
		lastMonthFirst := time.Date(y, m, 1, 0, 0, 0, 0, now.Location()).AddDate(0, -1, 0)
		due = append(due, [2]string{plugin.PeriodMonth, lastMonthFirst.Format(reportDayFormat)})
	}
	return due
}
