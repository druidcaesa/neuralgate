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

package oss

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// openMySQLForTest 打开并 ping MySQL；不可达时返回错误由调用方决定跳过
func openMySQLForTest(dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func newAdminUser(name string) *plugin.AdminUser {
	now := time.Now()
	return &plugin.AdminUser{
		ID:           "u-" + name,
		Username:     name,
		PasswordHash: "$2a$10$hash-" + name,
		Status:       plugin.AdminUserStatusActive,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

// assertAdminUserRoundTrip 校验三种实现的公共语义：计数、按用户名/ID 查询、upsert 更新
func assertAdminUserRoundTrip(t *testing.T, s plugin.StoragePlugin) {
	t.Helper()
	n, err := s.CountAdminUsers()
	if err != nil || n != 0 {
		t.Fatalf("CountAdminUsers() = %d, %v; want 0, nil", n, err)
	}
	u := newAdminUser("root")
	if err := s.SaveAdminUser(u); err != nil {
		t.Fatalf("SaveAdminUser: %v", err)
	}
	n, _ = s.CountAdminUsers()
	if n != 1 {
		t.Fatalf("CountAdminUsers() after save = %d, want 1", n)
	}
	got, err := s.GetAdminUserByUsername("root")
	if err != nil || got.ID != u.ID || got.PasswordHash != u.PasswordHash {
		t.Fatalf("GetAdminUserByUsername = %+v, %v", got, err)
	}
	if got.CreatedAt.UnixMilli() != u.CreatedAt.UnixMilli() {
		t.Errorf("CreatedAt round-trip mismatch: %v vs %v", got.CreatedAt, u.CreatedAt)
	}
	// LastLoginAt 指针字段往返
	loginAt := time.Now().Add(-time.Minute)
	u.LastLoginAt = &loginAt
	u.UpdatedAt = time.Now()
	if err := s.SaveAdminUser(u); err != nil {
		t.Fatalf("SaveAdminUser(upsert): %v", err)
	}
	got, err = s.GetAdminUserByUsername("root")
	if err != nil {
		t.Fatalf("re-get: %v", err)
	}
	if got.LastLoginAt == nil || got.LastLoginAt.UnixMilli() != loginAt.UnixMilli() {
		t.Errorf("LastLoginAt round-trip mismatch: %v vs %v", got.LastLoginAt, loginAt)
	}
	if n, _ = s.CountAdminUsers(); n != 1 {
		t.Errorf("upsert should not duplicate rows: count = %d", n)
	}
	// 按 ID 查询与未命中错误
	if _, err := s.GetAdminUserByID("u-root"); err != nil {
		t.Errorf("GetAdminUserByID: %v", err)
	}
	if _, err := s.GetAdminUserByID("missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetAdminUserByID(missing) error = %v, want ErrNotFound", err)
	}
	if _, err := s.GetAdminUserByUsername("missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetAdminUserByUsername(missing) error = %v, want ErrNotFound", err)
	}
}

func TestSQLStorageAdminUserCRUD(t *testing.T) {
	assertAdminUserRoundTrip(t, newTestSQLStorage(t))
}

func TestMemStorageAdminUserCRUD(t *testing.T) {
	assertAdminUserRoundTrip(t, NewMemStorage())
}

func TestDynamicStorageAdminUserDelegation(t *testing.T) {
	d := &dynamicStorage{}
	if err := d.Init(map[string]interface{}{"driver": "mem"}); err != nil {
		t.Fatal(err)
	}
	assertAdminUserRoundTrip(t, d)
}

// TestSQLStorageInitSQLitePragmas Init 后连接应启用 WAL 与 busy_timeout(经 DSN 注入)
func TestSQLStorageInitSQLitePragmas(t *testing.T) {
	s := NewSQLStorage()
	dsn := filepath.Join(t.TempDir(), "pragma-test.db")
	if err := s.Init(map[string]interface{}{"driver": "sqlite", "dsn": dsn}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer s.Close()
	var mode string
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
	var busy int64
	if err := s.db.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil {
		t.Fatal(err)
	}
	if busy != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", busy)
	}
}

// TestMySQLAuditColumnsMEDIUMTEXT_SkipWithoutDB MySQL 审计正文列应为 MEDIUMTEXT;
// 无真实 MySQL 时跳过(dsn 可用 NG_MYSQL_DSN 覆盖)
func TestMySQLAuditColumnsMEDIUMTEXT_SkipWithoutDB(t *testing.T) {
	dsn := os.Getenv("NG_MYSQL_DSN")
	if dsn == "" {
		dsn = "root:root@tcp(127.0.0.1:3306)/neuralgate?charset=utf8mb4"
	}
	db, err := openMySQLForTest(dsn)
	if err != nil {
		t.Skipf("mysql not reachable: %v", err)
	}
	defer db.Close()
	if err := mysqlCreateTables(db); err != nil {
		t.Fatalf("mysqlCreateTables: %v", err)
	}
	if err := ensureAuditColumnSizes(db); err != nil {
		t.Fatalf("ensureAuditColumnSizes: %v", err)
	}
	for _, col := range []string{"request_headers", "request_body", "response_body", "sse_chunks"} {
		var dataType string
		err := db.QueryRow(`SELECT DATA_TYPE FROM information_schema.COLUMNS
			WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'audit_logs' AND COLUMN_NAME = ?`, col).Scan(&dataType)
		if err != nil {
			t.Fatalf("column %s lookup: %v", col, err)
		}
		if dataType != "mediumtext" {
			t.Errorf("column %s type = %q, want mediumtext", col, dataType)
		}
	}
}
