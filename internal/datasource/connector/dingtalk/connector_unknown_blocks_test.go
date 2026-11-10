package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/logger"
)

// The unknown-type collection is a value on its own so the reporting path is
// testable without asserting on free-form log text: whatever this returns is
// exactly what the warning and the pipeline event carry.
func TestCollectUnknownBlockTypesReportsDocumentIdentity(t *testing.T) {
	document := node{
		ID:   "node-1",
		Name: "季度规划",
		URL:  "https://example.com/i/nodes/node-1",
	}
	rendered := renderResult{Markdown: "# 季度规划\n", UnknownTypes: []string{"attachment", "code"}}

	unknown, ok := collectUnknownBlockTypes(document, rendered)
	if !ok {
		t.Fatal("collectUnknownBlockTypes() reported a complete document")
	}
	if unknown.NodeID != "node-1" || unknown.Title != "季度规划" ||
		unknown.URL != "https://example.com/i/nodes/node-1" ||
		!reflect.DeepEqual(unknown.BlockTypes, []string{"attachment", "code"}) {
		t.Fatalf("unknownBlockTypes = %#v", unknown)
	}

	fields := unknown.fields()
	if fields["node_id"] != "node-1" || fields["title"] != "季度规划" ||
		fields["block_types"] != "attachment,code" || fields["count"] != 2 {
		t.Fatalf("fields() = %#v", fields)
	}
}

func TestCollectUnknownBlockTypesIsSilentWhenComplete(t *testing.T) {
	if _, ok := collectUnknownBlockTypes(node{ID: "node-1"}, renderResult{Markdown: "# ok\n"}); ok {
		t.Fatal("collectUnknownBlockTypes() reported loss for a fully rendered document")
	}
}

func TestUnknownBlockTypesTitleFallsBackToNodeID(t *testing.T) {
	unknown, ok := collectUnknownBlockTypes(
		node{ID: "node-2"},
		renderResult{UnknownTypes: []string{"code"}},
	)
	if !ok || unknown.Title != "node-2" {
		t.Fatalf("unknownBlockTypes = %#v, ok = %v", unknown, ok)
	}
}

// An unmodelled block type must stay visible on the fetch path: it keeps the
// metadata entry and additionally reaches the structured pipeline event that
// monitoring keys on. A block the API could not map carries only the real type
// name, and that name is what the metadata and the event must show. Only the
// stage/action pair and the document identity are asserted here, never the
// free-form message.
func TestFetchAllReportsUnknownBlockTypes(t *testing.T) {
	api := &fakeAPI{
		workspaces: []workspace{{ID: "space", RootNodeID: "root", Name: "Space"}},
		nodes: map[string][]node{
			"root": {{
				ID: "doc-unmapped", Type: "FILE", Category: "ALIDOC", Extension: "adoc",
				Name: "Release notes",
			}},
		},
		blocks: map[string][]json.RawMessage{
			"doc-unmapped": {rawJSON(`{"blockType":"unknown","unknown":{"rawType":"card"}}`)},
		},
		nodeErrors:  make(map[string]error),
		blockErrors: make(map[string]error),
	}

	var buf bytes.Buffer
	logger.SetOutput(&buf)
	defer logger.SetOutput(os.Stdout)

	items, err := testConnector(api).FetchAll(context.Background(), testConfig("space"), []string{"space"})
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("FetchAll() returned %d items, want 1", len(items))
	}
	if got := items[0].Metadata["unknown_block_types"]; got != "unknown:card" {
		t.Fatalf("metadata unknown_block_types = %q, want %q", got, "unknown:card")
	}
	for _, want := range []string{
		"stage=DingTalkConnector",
		"action=unknown_block_types",
		`node_id="doc-unmapped"`,
		`block_types="unknown:card"`,
	} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("pipeline event missing %q:\n%s", want, buf.String())
		}
	}
	t.Logf("emitted warnings:\n%s", strings.TrimSpace(buf.String()))
}

// A document whose blocks are all modelled must not emit the warning.
func TestFetchAllStaysQuietForModelledBlocks(t *testing.T) {
	api := &fakeAPI{
		workspaces: []workspace{{ID: "space", RootNodeID: "root", Name: "Space"}},
		nodes: map[string][]node{
			"root": {{
				ID: "doc-plain", Type: "FILE", Category: "ALIDOC", Extension: "adoc",
				Name: "Release notes",
			}},
		},
		blocks: map[string][]json.RawMessage{
			"doc-plain": {rawJSON(`{"blockType":"paragraph","paragraph":{"text":"all good"}}`)},
		},
		nodeErrors:  make(map[string]error),
		blockErrors: make(map[string]error),
	}

	var buf bytes.Buffer
	logger.SetOutput(&buf)
	defer logger.SetOutput(os.Stdout)

	items, err := testConnector(api).FetchAll(context.Background(), testConfig("space"), []string{"space"})
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("FetchAll() returned %d items, want 1", len(items))
	}
	if _, exists := items[0].Metadata["unknown_block_types"]; exists {
		t.Fatalf("metadata = %#v, want no unknown_block_types", items[0].Metadata)
	}
	if strings.Contains(buf.String(), "action=unknown_block_types") {
		t.Fatalf("unexpected warning for a fully rendered document:\n%s", buf.String())
	}
}
