// Package migrations 内嵌自研业务库的迁移 SQL，随二进制分发。
package migrations

import "embed"

// FS 包含所有 *.sql 迁移文件，供 golang-migrate iofs source 读取。
//
//go:embed *.sql
var FS embed.FS
