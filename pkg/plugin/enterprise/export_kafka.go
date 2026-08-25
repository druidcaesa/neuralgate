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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/twmb/franz-go/pkg/kgo"
)

// defaultKafkaTopic 未配置 topic 时使用的默认主题
const defaultKafkaTopic = "neuralgate-audit"

// KafkaTarget Kafka 外推：franz-go 真客户端，审计日志按条 Produce。
// key=request_id 保证同请求分区内有序；SASL/TLS 认证本期未接(api_key 字段对 kafka 忽略)，
// 后续扩展在 NewKafkaTarget 增加选项即可，接口形态不变
type KafkaTarget struct {
	client *kgo.Client
	topic  string
}

// NewKafkaTarget 创建 Kafka 目标；brokers 为逗号分隔 seed 列表，topic 空取默认值。
// 构造不阻塞(不立即连网)，连通性由 TestConnection 探测
func NewKafkaTarget(brokers, topic string) (*KafkaTarget, error) {
	brokers = strings.TrimSpace(brokers)
	if brokers == "" {
		return nil, errors.New("kafka brokers 不能为空")
	}
	if topic == "" {
		topic = defaultKafkaTopic
	}
	seedList := strings.Split(brokers, ",")
	for i := range seedList {
		seedList[i] = strings.TrimSpace(seedList[i])
	}
	client, err := kgo.NewClient(kgo.SeedBrokers(seedList...))
	if err != nil {
		return nil, fmt.Errorf("构造 kafka 客户端失败: %w", err)
	}
	return &KafkaTarget{client: client, topic: topic}, nil
}

// kafkaRecords 消息组装纯函数：每条日志一个 Record，key=request_id，value=JSON 序列化
func kafkaRecords(topic string, logs []*plugin.AuditLog) []*kgo.Record {
	records := make([]*kgo.Record, 0, len(logs))
	for _, log := range logs {
		value, err := json.Marshal(log)
		if err != nil {
			continue // 单条序列化失败跳过，不阻断整批
		}
		records = append(records, &kgo.Record{
			Topic: topic,
			Key:   []byte(log.RequestID),
			Value: value,
		})
	}
	return records
}

// Send 整批同步 Produce：任一分区写入失败即返回错误由上层退避重试
func (t *KafkaTarget) Send(logs []*plugin.AuditLog) error {
	records := kafkaRecords(t.topic, logs)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := t.client.ProduceSync(ctx, records...).FirstErr(); err != nil {
		return fmt.Errorf("kafka 生产失败: %w", err)
	}
	return nil
}

// TestConnection 探测 broker 集群可达
func (t *KafkaTarget) TestConnection() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := t.client.Ping(ctx); err != nil {
		return fmt.Errorf("kafka 集群不可达: %w", err)
	}
	return nil
}

// Close 释放底层连接
func (t *KafkaTarget) Close() error {
	t.client.Close()
	return nil
}
