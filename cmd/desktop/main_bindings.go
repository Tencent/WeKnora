//go:build bindings

package main

import (
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
)

// Wails「生成绑定」阶段使用 -tags bindings 单独编译本文件，不启动 Gin/数据库，避免依赖本机 Postgres。
func main() {
	app := NewApp()
	_ = wails.Run(&options.App{
		Title: "WeKnora Lite",
		Bind:  []interface{}{app},
	})
}
