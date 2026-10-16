package dingtalk

import (
	"encoding/json"
	"testing"
)

// A list block can carry inline children (the item text) and block children
// (one per item) at the same time. Both kinds must survive rendering: an inline
// item used to shadow every block item that followed it.
func TestRenderListKeepsInlineAndBlockItems(t *testing.T) {
	result := renderDocument("测试文档", []json.RawMessage{
		rawJSON(`{"blockType":"unorderedList","unorderedList":{"list":{"level":0}},"children":[` +
			`{"elementType":"text","text":"第一项"},` +
			`{"blockType":"paragraph","paragraph":{"text":"第二项整段内容"}}]}`),
	})

	want := "# 测试文档\n\n- 第一项\n- 第二项整段内容\n"
	if result.Markdown != want {
		t.Fatalf("Markdown = %q, want %q", result.Markdown, want)
	}
	if len(result.UnknownTypes) != 0 {
		t.Fatalf("UnknownTypes = %#v, want none", result.UnknownTypes)
	}
}

// Items delivered as block children must keep one bullet line each instead of
// being flattened onto a single line.
func TestRenderListEmitsOneBulletPerBlockItem(t *testing.T) {
	result := renderDocument("测试文档", []json.RawMessage{
		rawJSON(`{"blockType":"unorderedList","unorderedList":{"list":{"level":0}},"children":[` +
			`{"blockType":"paragraph","paragraph":{"text":"甲项"}},` +
			`{"blockType":"paragraph","paragraph":{"text":"乙项"}},` +
			`{"blockType":"paragraph","paragraph":{"text":"丙项"}}]}`),
	})

	want := "# 测试文档\n\n- 甲项\n- 乙项\n- 丙项\n"
	if result.Markdown != want {
		t.Fatalf("Markdown = %q, want %q", result.Markdown, want)
	}
}

// Inline and block children keep their payload order, so an inline run that
// follows a block item is not hoisted above it or merged into another item.
func TestRenderListKeepsItemOrder(t *testing.T) {
	result := renderDocument("", []json.RawMessage{
		rawJSON(`{"blockType":"orderedList","orderedList":{"list":{"level":0}},"children":[` +
			`{"blockType":"paragraph","paragraph":{"text":"块项"}},` +
			`{"elementType":"text","text":"内联项一"},` +
			`{"elementType":"text","text":"内联项二"}]}`),
	})

	want := "1. 块项\n1. 内联项一内联项二\n"
	if result.Markdown != want {
		t.Fatalf("Markdown = %q, want %q", result.Markdown, want)
	}
}

// Nested lists are indented by their own level, and never sit at or above the
// parent list even when the payload repeats the parent level.
func TestRenderListIndentsNestedLists(t *testing.T) {
	result := renderDocument("", []json.RawMessage{
		rawJSON(`{"blockType":"unorderedList","unorderedList":{"list":{"level":0}},"children":[` +
			`{"elementType":"text","text":"父项"},` +
			`{"blockType":"unorderedList","unorderedList":{"list":{"level":1}},"children":[` +
			`{"elementType":"text","text":"子项"}]},` +
			`{"blockType":"unorderedList","unorderedList":{"list":{"level":0}},"children":[` +
			`{"elementType":"text","text":"孙项同层级"}]}]}`),
	})

	want := "- 父项\n  - 子项\n  - 孙项同层级\n"
	if result.Markdown != want {
		t.Fatalf("Markdown = %q, want %q", result.Markdown, want)
	}
}

// A multi-line item stays inside its bullet: continuation lines are aligned
// under the first line rather than escaping to the document root.
func TestRenderListIndentsContinuationLines(t *testing.T) {
	// A document title keeps the leading indentation of the first list from
	// being trimmed away, matching how these lists appear in real documents.
	result := renderDocument("测试文档", []json.RawMessage{
		rawJSON(`{"blockType":"unorderedList","unorderedList":{"list":{"level":1}},"children":[` +
			`{"blockType":"paragraph","paragraph":{"text":"第一行\n第二行"}}]}`),
	})

	want := "# 测试文档\n\n  - 第一行\n    第二行\n"
	if result.Markdown != want {
		t.Fatalf("Markdown = %q, want %q", result.Markdown, want)
	}
}

// The documented list shape is flat: one block per item, nesting carried by
// list.level, the items of one list linked by a shared listId across sibling
// blocks. This pins that main path — two sibling items at different levels —
// so the defensive block-child handling above cannot become the assumed model.
func TestRenderListFollowsTheFlatDocumentedShape(t *testing.T) {
	result := renderDocument("", []json.RawMessage{
		rawJSON(`{"blockType":"unorderedList","unorderedList":{"list":{"listId":"list-1","level":0}},` +
			`"children":[{"text":"父项"}]}`),
		rawJSON(`{"blockType":"unorderedList","unorderedList":{"list":{"listId":"list-1","level":1}},` +
			`"children":[{"text":"子项"}]}`),
		rawJSON(`{"blockType":"unorderedList","unorderedList":{"list":{"listId":"list-2","level":0}},` +
			`"children":[{"text":"另一组的项"}]}`),
	})

	want := "- 父项\n  - 子项\n- 另一组的项\n"
	if result.Markdown != want {
		t.Fatalf("Markdown = %q, want %q", result.Markdown, want)
	}
	if len(result.UnknownTypes) != 0 {
		t.Fatalf("UnknownTypes = %#v, want none", result.UnknownTypes)
	}
}
