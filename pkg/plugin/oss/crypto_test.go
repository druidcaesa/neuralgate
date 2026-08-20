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

import "testing"

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := "test-key"
	plain := "sk-abc123"
	enc, err := Encrypt(plain, key)
	if err != nil {
		t.Fatalf("Encrypt error: %v", err)
	}
	if enc == plain {
		t.Fatal("ciphertext must differ from plaintext")
	}
	dec, err := Decrypt(enc, key)
	if err != nil {
		t.Fatalf("Decrypt error: %v", err)
	}
	if dec != plain {
		t.Fatalf("roundtrip mismatch: got %q want %q", dec, plain)
	}
}

func TestDecryptWrongKeyFails(t *testing.T) {
	enc, err := Encrypt("secret", "key-a")
	if err != nil {
		t.Fatalf("Encrypt error: %v", err)
	}
	if _, err := Decrypt(enc, "key-b"); err == nil {
		t.Fatal("decrypt with wrong key must fail")
	}
}

func TestDecryptInvalidCiphertextFails(t *testing.T) {
	if _, err := Decrypt("not-valid-ciphertext", "key"); err == nil {
		t.Fatal("decrypt invalid input must fail")
	}
}
