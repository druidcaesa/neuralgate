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
	"encoding/json"
	"os"
	"testing"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// TestNewKafkaTargetRejectsEmptyBrokers 空 broker 列表必须构造失败
func TestNewKafkaTargetRejectsEmptyBrokers(t *testing.T) {
	if _, err := NewKafkaTarget("", ""); err == nil {
		t.Fatal("空 brokers 应报错")
	}
}

// TestKafkaRecords 消息组装纯函数：topic/key/value 三要素正确，value 可反序列化回 AuditLog
func TestKafkaRecords(t *testing.T) {
	logs := []*plugin.AuditLog{
		{RequestID: "req-1", ModelName: "gpt-test", TotalTokens: 12},
		{RequestID: "req-2", ModelName: "qwen"},
	}
	records := kafkaRecords("audit-topic", logs)
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	for i, rec := range records {
		if rec.Topic != "audit-topic" {
			t.Errorf("record[%d].Topic = %s", i, rec.Topic)
		}
		if string(rec.Key) != logs[i].RequestID {
			t.Errorf("record[%d].Key = %s, want request_id", i, rec.Key)
		}
		var decoded plugin.AuditLog
		if err := json.Unmarshal(rec.Value, &decoded); err != nil {
			t.Errorf("record[%d] value 非合法 AuditLog JSON: %v", i, err)
		}
		if decoded.RequestID != logs[i].RequestID {
			t.Errorf("record[%d] value.RequestID = %s", i, decoded.RequestID)
		}
	}
}

// TestKafkaTargetDefaultTopic 空 topic 落到默认值
func TestKafkaTargetDefaultTopic(t *testing.T) {
	target, err := NewKafkaTarget("localhost:9092", "")
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	defer target.Close()
	if target.topic != defaultKafkaTopic {
		t.Errorf("默认 topic = %q, want %q", target.topic, defaultKafkaTopic)
	}
}

// TestFactoryRegistersKafka 工厂识别 kafka 类型并透传 topic
func TestFactoryRegistersKafka(t *testing.T) {
	target, err := NewExportTarget("kafka", "localhost:9092", "", "custom-topic")
	if err != nil {
		t.Fatalf("工厂不支持 kafka: %v", err)
	}
	kt, ok := target.(*KafkaTarget)
	if !ok {
		t.Fatalf("应返回 KafkaTarget, got %T", target)
	}
	if kt.topic != "custom-topic" {
		t.Errorf("topic 未透传: %q", kt.topic)
	}
}

// TestKafkaTargetRealBroker 真连测试：NG_KAFKA_BROKER 设置时执行(如 docker 单节点 KRaft)
func TestKafkaTargetRealBroker(t *testing.T) {
	brokers := os.Getenv("NG_KAFKA_BROKER")
	if brokers == "" {
		t.Skip("未设置 NG_KAFKA_BROKER,跳过真连测试")
	}
	target, err := NewKafkaTarget(brokers, "neuralgate-e2e-test")
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	defer target.Close()
	if err := target.TestConnection(); err != nil {
		t.Fatalf("Ping 失败: %v", err)
	}
	logs := []*plugin.AuditLog{{RequestID: "e2e-1", ModelName: "m"}}
	if err := target.Send(logs); err != nil {
		t.Fatalf("Send 失败: %v", err)
	}
}
