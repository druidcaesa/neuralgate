//go:build enterprise

package main

import (
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/enterprise"
)

const edition = "enterprise"

// newPluginFactory 由 BuildTag 决定返回哪个版本的插件工厂
func newPluginFactory() plugin.PluginFactory {
	return enterprise.NewPluginFactory()
}
