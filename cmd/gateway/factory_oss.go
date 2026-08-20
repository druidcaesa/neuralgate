//go:build !enterprise

package main

import (
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

const edition = "oss"

// newPluginFactory 由 BuildTag 决定返回哪个版本的插件工厂
func newPluginFactory() plugin.PluginFactory {
	return oss.NewPluginFactory()
}
