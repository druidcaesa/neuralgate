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
	"testing"
)

func TestFactoryCreateStorageByDriver(t *testing.T) {
	f := NewPluginFactory()

	mem := f.CreateStorage()
	if err := mem.Init(map[string]interface{}{"driver": "mem"}); err != nil {
		t.Fatalf("mem init: %v", err)
	}
	if ds, ok := mem.(*dynamicStorage); !ok {
		t.Fatalf("got %T; want *dynamicStorage", mem)
	} else if _, ok := ds.impl.(*MemStorage); !ok {
		t.Fatalf("driver=mem got %T; want *MemStorage", ds.impl)
	}

	sqlite := f.CreateStorage()
	if err := sqlite.Init(map[string]interface{}{
		"driver": "sqlite", "dsn": ":memory:", "encrypt_key": "test",
	}); err != nil {
		t.Fatalf("sqlite init: %v", err)
	}
	if ds, ok := sqlite.(*dynamicStorage); !ok {
		t.Fatalf("got %T; want *dynamicStorage", sqlite)
	} else if _, ok := ds.impl.(*SQLStorage); !ok {
		t.Fatalf("driver=sqlite got %T; want *SQLStorage", ds.impl)
	}

	// 未知驱动报错
	bad := f.CreateStorage()
	if err := bad.Init(map[string]interface{}{"driver": "oracle"}); err == nil {
		t.Fatal("unknown driver must error")
	}
}
