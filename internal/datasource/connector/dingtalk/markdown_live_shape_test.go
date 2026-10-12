package dingtalk

import (
	"encoding/json"
	"testing"
)

// Fixtures in this file are **live payloads**, copied from
// `GET /v1.0/doc/suites/documents/{id}/blocks` against a production tenant and
// only shortened where marked. They exist because the list renderer was once
// written against a shape the API never returns — `children` — while the real
// response carries one item per block with the text in the block's own field.
// Nine production documents, every list block: no `children`, no `list.level`,
// no `listId`. A fixture invented from the docs cannot catch that; these can.

// The live shape of an unordered list item: the whole item is `text`, nothing else.
func TestRenderListReadsTheLiveFlatShape(t *testing.T) {
	result := renderDocument("测试文档", []json.RawMessage{
		rawJSON(`{"blockType":"unorderedList","index":87,"id":"m050v0j3o1iic0pr7xg",` +
			`"unorderedList":{"text":"确认是否自定义全端口，默认是常见端口"}}`),
		rawJSON(`{"blockType":"orderedList","index":88,"id":"m050w04e41a0sfbcy1",` +
			`"orderedList":{"text":"第一步：确认资产范围"}}`),
	})

	want := "# 测试文档\n\n- 确认是否自定义全端口，默认是常见端口\n1. 第一步：确认资产范围\n"
	if result.Markdown != want {
		t.Fatalf("Markdown = %q, want %q", result.Markdown, want)
	}
	if len(result.UnknownTypes) != 0 {
		t.Fatalf("UnknownTypes = %#v, want none", result.UnknownTypes)
	}
}

// Consecutive live list blocks are consecutive items of one list, and every one
// of them must survive. This is the regression that shipped: 190 of 190 list
// items were dropped, silently, because the type was "known" and so never
// showed up in the unknown-type report either.
func TestRenderListKeepsEveryItemOfALiveDocument(t *testing.T) {
	result := renderDocument("测试文档", []json.RawMessage{
		rawJSON(`{"blockType":"unorderedList","index":1,"id":"a","unorderedList":{"text":"项目分享（团队版中）"}}`),
		rawJSON(`{"blockType":"unorderedList","index":2,"id":"b","unorderedList":{"text":"项目报告导出"}}`),
		rawJSON(`{"blockType":"unorderedList","index":3,"id":"c","unorderedList":{"text":"任务下发"}}`),
	})

	want := "# 测试文档\n\n- 项目分享（团队版中）\n- 项目报告导出\n- 任务下发\n"
	if result.Markdown != want {
		t.Fatalf("Markdown = %q, want %q", result.Markdown, want)
	}
}

// Multi-line items keep their continuation lines aligned under the first line,
// so the value cannot escape its bullet.
func TestRenderListKeepsLiveMultiLineItem(t *testing.T) {
	result := renderDocument("测试文档", []json.RawMessage{
		rawJSON(`{"blockType":"orderedList","index":4,"id":"d",` +
			`"orderedList":{"text":"连接到内网探测模块：\n执行所下载的探测Agent"}}`),
	})

	want := "# 测试文档\n\n1. 连接到内网探测模块：\n   执行所下载的探测Agent\n"
	if result.Markdown != want {
		t.Fatalf("Markdown = %q, want %q", result.Markdown, want)
	}
}

// The live `sdt` block is a table of contents and carries the whole thing in
// `text`. The type is still unmodelled, so the marker must stay — but dropping
// the text because the presentation is unknown loses the document's contents
// page. Verified live: 1,212 characters in one block.
func TestRenderUnknownBlockRendersItsLiveText(t *testing.T) {
	result := renderDocument("测试文档", []json.RawMessage{
		rawJSON(`{"blockType":"unknown","index":19,"id":"lqaap37i2lv2yn7i5w7",` +
			`"unknown":{"rawType":"sdt","text":"目录目录1前言11. 产品介绍1"}}`),
	})

	want := "# 测试文档\n\n目录目录1前言11. 产品介绍1\n"
	if result.Markdown != want {
		t.Fatalf("Markdown = %q, want %q", result.Markdown, want)
	}
	if len(result.UnknownTypes) != 1 || result.UnknownTypes[0] != "unknown:sdt" {
		t.Fatalf("UnknownTypes = %#v, want [unknown:sdt]", result.UnknownTypes)
	}
}

// Not every unmodelled block has text: the live `card` and `sectionBr` blocks
// carry only `rawType`, so they stay a marker and render nothing.
func TestRenderUnknownBlockWithoutTextStaysAMarker(t *testing.T) {
	result := renderDocument("测试文档", []json.RawMessage{
		rawJSON(`{"blockType":"unknown","index":21,"id":"mulaiyzgurq2s6o7b2f",` +
			`"unknown":{"rawType":"sectionBr"}}`),
	})

	want := "# 测试文档\n"
	if result.Markdown != want {
		t.Fatalf("Markdown = %q, want %q", result.Markdown, want)
	}
	if len(result.UnknownTypes) != 1 || result.UnknownTypes[0] != "unknown:sectionBr" {
		t.Fatalf("UnknownTypes = %#v, want [unknown:sectionBr]", result.UnknownTypes)
	}
}

// A block carrying both its own text and inline children is a shape the API does
// not produce. Emitting both would duplicate the item, so the shape is reported
// instead of guessed at.
func TestRenderListFlagsOwnTextAndInlineChildrenTogether(t *testing.T) {
	result := renderDocument("测试文档", []json.RawMessage{
		rawJSON(`{"blockType":"unorderedList","unorderedList":{"text":"甲"},` +
			`"children":[{"elementType":"text","text":"甲"}]}`),
	})

	want := "# 测试文档\n\n- 甲\n"
	if result.Markdown != want {
		t.Fatalf("Markdown = %q, want %q", result.Markdown, want)
	}
	if len(result.UnknownTypes) != 1 || result.UnknownTypes[0] != "list_text_and_inline_children" {
		t.Fatalf("UnknownTypes = %#v, want [list_text_and_inline_children]", result.UnknownTypes)
	}
}

// Escaping still applies to the block's own text, so an item that starts with a
// marker character cannot turn into a nested list or a heading.
func TestRenderListEscapesLiveOwnText(t *testing.T) {
	result := renderDocument("测试文档", []json.RawMessage{
		rawJSON(`{"blockType":"unorderedList","unorderedList":{"text":"- 不是子列表"}}`),
	})

	want := "# 测试文档\n\n- \\- 不是子列表\n"
	if result.Markdown != want {
		t.Fatalf("Markdown = %q, want %q", result.Markdown, want)
	}
}
