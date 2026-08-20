package adapter

import (
	"errors"
	"sync"
)

// ErrAdapterNotFound 适配器未注册
var ErrAdapterNotFound = errors.New("adapter not found")

// AdapterRegistry 模型适配器注册中心（照设计文档 8.1）
type AdapterRegistry struct {
	adapters map[string]ModelAdapter // provider -> adapter
	mu       sync.RWMutex
}

// NewAdapterRegistry 创建注册中心
func NewAdapterRegistry() *AdapterRegistry {
	return &AdapterRegistry{adapters: make(map[string]ModelAdapter)}
}

// Register 注册适配器（同名覆盖）
func (r *AdapterRegistry) Register(adapter ModelAdapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[adapter.Name()] = adapter
}

// Get 按 provider 获取适配器
func (r *AdapterRegistry) Get(provider string) (ModelAdapter, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	adapter, ok := r.adapters[provider]
	if !ok {
		return nil, ErrAdapterNotFound
	}
	return adapter, nil
}
