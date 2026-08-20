package core

// VersionInfo 版本信息（照设计文档 7.3）
type VersionInfo struct {
	Version   string   // 版本号
	BuildTime string   // 编译时间
	GitCommit string   // Git提交
	Edition   string   // 版本类型：oss/enterprise
	Features  []string // 可用功能列表
}

// 以下变量通过 Makefile -ldflags 注入
var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)
