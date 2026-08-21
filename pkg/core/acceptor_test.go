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

package core

import "testing"

func TestIPFilterDisabled(t *testing.T) {
	f := NewIPFilter("disabled", nil, nil)
	if !f.Allow("1.2.3.4") {
		t.Fatal("disabled mode should allow all")
	}
}

func TestIPFilterWhitelist(t *testing.T) {
	f := NewIPFilter("whitelist", []string{"10.0.0.0/8", "192.168.1.100"}, nil)
	if !f.Allow("10.5.6.7") {
		t.Fatal("10.5.6.7 in 10.0.0.0/8 should be allowed")
	}
	if !f.Allow("192.168.1.100") {
		t.Fatal("exact IP should be allowed")
	}
	if f.Allow("8.8.8.8") {
		t.Fatal("8.8.8.8 not in whitelist should be denied")
	}
}

func TestIPFilterBlacklist(t *testing.T) {
	f := NewIPFilter("blacklist", nil, []string{"1.2.3.0/24"})
	if f.Allow("1.2.3.4") {
		t.Fatal("1.2.3.4 in blacklist should be denied")
	}
	if !f.Allow("5.6.7.8") {
		t.Fatal("5.6.7.8 not in blacklist should be allowed")
	}
}

func TestIPFilterInvalidIPDenied(t *testing.T) {
	f := NewIPFilter("whitelist", []string{"10.0.0.0/8"}, nil)
	if f.Allow("not-an-ip") {
		t.Fatal("unparseable IP in whitelist mode should be denied")
	}
}
