//go:build !cgo

package types

import "strings"

type jiebaStub struct{}

func (*jiebaStub) Cut(s string, _ bool) []string {
	return strings.Fields(s)
}

func (*jiebaStub) CutForSearch(s string, _ bool) []string {
	return strings.Fields(s)
}

// Jieba provides a pure-Go fallback tokenizer when built with CGO_ENABLED=0
// (e.g. cross-platform compile checks). Production builds run with cgo enabled.
var Jieba = &jiebaStub{}
