package chunker

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func withSplitter(t *testing.T, f PluginSplitter) {
	t.Helper()
	prev := pluginSplitter.Load()
	SetPluginSplitter(f)
	t.Cleanup(func() {
		if prev != nil {
			pluginSplitter.Store(prev)
		} else {
			pluginSplitter.Store(nil)
		}
	})
}

func TestPluginChunkerCutsAndFallsBack(t *testing.T) {
	text := strings.Repeat("Clause one says this. ", 20) + "\n\n" + strings.Repeat("Clause two says that. ", 20)
	n := len([]rune(text))
	half := strings.Index(text, "\n\n")
	var gotChunker string
	var gotSize int
	withSplitter(t, func(ctx context.Context, chunker, _ string, cfg SplitterConfig) ([]Span, error) {
		gotChunker, gotSize = chunker, cfg.ChunkSize
		if ctx.Value(ctxKey{}) != "tenant-7" {
			return nil, errors.New("lost the caller's context")
		}
		return []Span{{Start: 0, End: half, ContextHeader: "Clause 1"}, {Start: half + 2, End: n}}, nil
	})
	ctx := context.WithValue(context.Background(), ctxKey{}, "tenant-7")
	cfg := WithPlugins(ctx, SplitterConfig{Strategy: "plugin:acme.legal/clauses", ChunkSize: 512})
	chunks, diag := SplitWithDiagnostics(text, cfg)
	if diag.SelectedTier != TierPlugin || gotChunker != "acme.legal/clauses" || gotSize != 512 {
		t.Fatalf("tier %s, chunker %q, size %d", diag.SelectedTier, gotChunker, gotSize)
	}
	if len(chunks) != 2 || chunks[0].ContextHeader != "Clause 1" || chunks[1].Seq != 1 ||
		chunks[1].Content != string([]rune(text)[half+2:]) {
		t.Fatalf("chunks = %+v", chunks)
	}

	// Out-of-range spans, errors and a missing splitter fall back.
	for _, f := range []PluginSplitter{
		func(context.Context, string, string, SplitterConfig) ([]Span, error) {
			return []Span{{Start: 0, End: n + 5}}, nil
		},
		func(context.Context, string, string, SplitterConfig) ([]Span, error) { return nil, errors.New("down") },
		func(context.Context, string, string, SplitterConfig) ([]Span, error) {
			return []Span{{Start: 0, End: n}}, nil
		},
	} {
		withSplitter(t, f)
		cfg := WithPlugins(ctx, SplitterConfig{Strategy: "plugin:x/y", ChunkSize: 100})
		chunks, diag := SplitWithDiagnostics(text, cfg)
		if diag.SelectedTier == TierPlugin || len(chunks) == 0 {
			t.Fatalf("fallback: tier %s, %d chunks", diag.SelectedTier, len(chunks))
		}
	}
	// Without WithPlugins the strategy just means auto.
	if _, diag := SplitWithDiagnostics(text, SplitterConfig{Strategy: "plugin:x/y"}); diag.SelectedTier == TierPlugin {
		t.Fatal("a config without the plugin hook used the plugin")
	}
}

func TestPluginChunkerCutsParentsOnly(t *testing.T) {
	calls := 0
	text := strings.Repeat("Sentence number one is here. ", 80)
	n := len([]rune(text))
	withSplitter(t, func(context.Context, string, string, SplitterConfig) ([]Span, error) {
		calls++
		return []Span{{Start: 0, End: n / 2}, {Start: n / 2, End: n}}, nil
	})
	base := WithPlugins(context.Background(), SplitterConfig{Strategy: "plugin:x/y"})
	parent, child := DeriveParentChildConfigs(base, 1200, 300)
	res := SplitParentChild(text, parent, child)
	if calls != 1 || len(res.Children) < 2 {
		t.Fatalf("calls %d, children %d", calls, len(res.Children))
	}
}

type ctxKey struct{}
