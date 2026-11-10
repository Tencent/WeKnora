// Package runtime 提供应用程序运行时的依赖注入容器
// 该包使用 uber 的 dig 库来管理依赖项注入
package runtime

import (
	"go.uber.org/dig"
)

// container 是应用程序的全局依赖注入容器
// 所有服务和组件都通过它进行注册和解析
var container *dig.Container

// init 初始化依赖注入容器
// 在程序启动时自动调用
func init() {
	container = dig.New()
}

// GetContainer 返回全局依赖注入容器的引用
// 供其他包使用以注册或获取服务
func GetContainer() *dig.Container {
	return container
}
