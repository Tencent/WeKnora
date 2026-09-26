package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// wantCall is one call of the vendor's non-stream ground truth.
type wantCall struct {
	Name      string
	Arguments string
}

// stripIndex rebuilds captured deltas the way a gateway that drops
// delta.tool_calls[].index sends them: no index at all, one entry per chunk.
func stripIndex(base []ToolCallDelta) []ToolCallDelta {
	out := make([]ToolCallDelta, len(base))
	for i, tc := range base {
		tc.Index, tc.IndexMissing = 0, true
		out[i] = tc
	}
	return out
}

// stripID removes the call ids, leaving the name as the only way to tell two
// calls apart (a gateway that rewrites the entries and loses both).
func stripID(deltas []ToolCallDelta) []ToolCallDelta {
	out := make([]ToolCallDelta, len(deltas))
	for i, tc := range deltas {
		tc.ID = ""
		out[i] = tc
	}
	return out
}

// repeatID stamps the owning call's id on every fragment, the other common
// gateway shape. The captured vendor index identifies the owning call.
func repeatID(base []ToolCallDelta) []ToolCallDelta {
	byIndex := map[int]string{}
	for _, tc := range base {
		if tc.ID != "" {
			byIndex[tc.Index] = tc.ID
		}
	}
	out := make([]ToolCallDelta, len(base))
	for i, tc := range base {
		id := byIndex[tc.Index]
		tc.Index, tc.IndexMissing = 0, true
		tc.ID = id
		out[i] = tc
	}
	return out
}

// chunk groups deltas into chunks of at most n entries, the way a gateway that
// batches fragments sends them. A batch may straddle a call boundary.
func chunk(deltas []ToolCallDelta, n int) []Delta {
	if n < 1 {
		n = 1
	}
	out := make([]Delta, 0, len(deltas)/n+1)
	for start := 0; start < len(deltas); start += n {
		end := start + n
		if end > len(deltas) {
			end = len(deltas)
		}
		out = append(out, Delta{ToolCalls: deltas[start:end]})
	}
	return out
}

// indexlessGatewayShapes are the gateway behaviours the fix has to survive. The
// first row is the baseline that must not change: with an index present the
// vendor index stays authoritative.
var indexlessGatewayShapes = []struct {
	name  string
	build func(base []ToolCallDelta) []Delta
}{
	{
		name:  "vendor index present, one entry per chunk",
		build: func(base []ToolCallDelta) []Delta { return chunk(base, 1) },
	},
	{
		name:  "index dropped, one entry per chunk",
		build: func(base []ToolCallDelta) []Delta { return chunk(stripIndex(base), 1) },
	},
	{
		name:  "index dropped, two entries per chunk",
		build: func(base []ToolCallDelta) []Delta { return chunk(stripIndex(base), 2) },
	},
	{
		name:  "index dropped, whole payload in one chunk",
		build: func(base []ToolCallDelta) []Delta { return chunk(stripIndex(base), len(base)) },
	},
	{
		name:  "index dropped, id repeated on every fragment",
		build: func(base []ToolCallDelta) []Delta { return chunk(repeatID(base), 1) },
	},
	{
		name:  "index and id dropped",
		build: func(base []ToolCallDelta) []Delta { return chunk(stripIndex(stripID(base)), 1) },
	},
	{
		name:  "index dropped, whole payload in one chunk with repeated id",
		build: func(base []ToolCallDelta) []Delta { return chunk(repeatID(base), len(base)) },
	},
}

// Every shape a gateway can present must assemble to the non-stream ground
// truth, for two calls of the same tool and for two different tools. The
// pre-fix fallback (slot = entry position inside the chunk) collapsed every
// index-less shape into a single call per stream: names concatenated and the
// arguments became invalid JSON.
func TestStreamAssembler_IndexlessToolCallsMatchGroundTruth(t *testing.T) {
	for key, base := range indexlessBaseDeltas {
		want := indexlessWantCalls[key]
		for _, shape := range indexlessGatewayShapes {
			t.Run(key+"/"+shape.name, func(t *testing.T) {
				a := NewStreamAssembler(context.Background(), "m")
				ch := make(chan types.StreamResponse, 8*len(base)+8)
				for _, d := range shape.build(base) {
					a.Process(ch, d)
				}

				got := a.OrderedToolCalls()
				if len(got) != len(want) {
					t.Fatalf("assembled %d calls %#v, want %d", len(got), got, len(want))
				}
				for i := range want {
					if got[i].Function.Name != want[i].Name {
						t.Errorf("call %d name = %q, want %q", i, got[i].Function.Name, want[i].Name)
					}
					if got[i].Function.Arguments != want[i].Arguments {
						t.Errorf("call %d arguments = %q, want %q",
							i, got[i].Function.Arguments, want[i].Arguments)
					}
					if !json.Valid([]byte(got[i].Function.Arguments)) {
						t.Errorf("call %d arguments are not valid JSON: %q",
							i, got[i].Function.Arguments)
					}
				}
			})
		}
	}
}

// Named regression for the reported failure. With tool_calls[].index absent the
// chunk-local fallback sent every fragment to slot 0, so the two parallel calls
// came out as one call named "wiki_read_pagewiki_replace_text" whose arguments
// were the two payloads glued together ("{\"slug\":\"g\"}{\"slug\":\"g\"}") —
// invalid JSON, which made the whole tool round fail.
func TestStreamAssembler_IndexlessParallelCallsDoNotCollapse(t *testing.T) {
	stream := []Delta{
		{ToolCalls: []ToolCallDelta{{
			IndexMissing: true, ID: "call_read", Name: "wiki_read_page",
		}}},
		{ToolCalls: []ToolCallDelta{{IndexMissing: true, Arguments: `{"slug":`}}},
		{ToolCalls: []ToolCallDelta{{IndexMissing: true, Arguments: `"g"}`}}},
		{ToolCalls: []ToolCallDelta{{
			IndexMissing: true, ID: "call_replace", Name: "wiki_replace_text",
		}}},
		{ToolCalls: []ToolCallDelta{{IndexMissing: true, Arguments: `{"slug":`}}},
		{ToolCalls: []ToolCallDelta{{IndexMissing: true, Arguments: `"g"}`}}},
	}

	a := NewStreamAssembler(context.Background(), "m")
	ch := make(chan types.StreamResponse, 32)
	for _, d := range stream {
		a.Process(ch, d)
	}

	got := a.OrderedToolCalls()
	if len(got) != 2 {
		t.Fatalf("assembled %d calls, want 2 (parallel calls collapsed): %#v", len(got), got)
	}
	wantNames := []string{"wiki_read_page", "wiki_replace_text"}
	for i, name := range wantNames {
		if got[i].Function.Name != name {
			t.Fatalf("call %d name = %q, want %q (collapse produced %q)",
				i, got[i].Function.Name, name, got[0].Function.Name)
		}
		if got[i].Function.Arguments != `{"slug":"g"}` {
			t.Fatalf("call %d arguments = %q, want %q",
				i, got[i].Function.Arguments, `{"slug":"g"}`)
		}
		if !json.Valid([]byte(got[i].Function.Arguments)) {
			t.Fatalf("call %d arguments are not valid JSON: %q", i, got[i].Function.Arguments)
		}
	}
}

// Some chunks carry the vendor index and some do not. An index-less fragment
// continues the call the previous chunk resolved, and a slot invented for a new
// call must not collide with an index the vendor already used.
func TestStreamAssembler_MixedIndexPresenceKeepsCurrentCall(t *testing.T) {
	a := NewStreamAssembler(context.Background(), "m")
	ch := make(chan types.StreamResponse, 32)

	a.Process(ch, Delta{ToolCalls: []ToolCallDelta{{
		Index: 3, ID: "call_a", Name: "f", Arguments: `{"a"`,
	}}})
	a.Process(ch, Delta{ToolCalls: []ToolCallDelta{{IndexMissing: true, Arguments: `:1}`}}})
	a.Process(ch, Delta{ToolCalls: []ToolCallDelta{{
		IndexMissing: true, ID: "call_b", Name: "g", Arguments: `{"b":2}`,
	}}})

	got := a.OrderedToolCalls()
	if len(got) != 2 {
		t.Fatalf("assembled %d calls %#v, want 2", len(got), got)
	}
	if got[0].Function.Name != "f" || got[0].Function.Arguments != `{"a":1}` {
		t.Fatalf("call 0 = %#v, want f/{\"a\":1}: the index-less fragment did not continue the current call",
			got[0].Function)
	}
	if got[1].Function.Name != "g" {
		t.Fatalf("call 1 = %#v, want g", got[1].Function)
	}
	// The vendor used slot 3, so the invented slot must be 4, not 0/1.
	if _, ok := a.toolCallMap[4]; !ok {
		t.Fatalf("new call landed on %d, want max seen index + 1 = 4", a.currentToolCallIndex)
	}
}
