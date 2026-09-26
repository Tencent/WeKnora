package activate

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/pkg"
	"github.com/Tencent/WeKnora/internal/plugin/plugintest"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pluginsdk"
	"github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

const guardManifest = `schemaVersion: 1
id: acme.guard
version: 1.0.0
apiVersion: weknora.plugin/v1
name: { en-US: ACME Guard }
publisher: { id: acme }
runtime: { type: host, kind: binary, entry: bin/guard }
contributes:
  pipelineHooks:
    - id: guard
      name: { en-US: Guard }
      stages: [rewriteQuery, filterResults, answer]
`

func TestPipelineHooks(t *testing.T) {
	var sawTenant uint64
	plugin := pluginsdk.New(pluginsdk.Info{ID: "acme.guard", Version: "1.0.0"})
	plugin.PipelineHook("guard", pluginsdk.PipelineHook{
		RewriteQuery: func(_ context.Context, call *pluginsdk.Call,
			in pluginapi.RewriteQueryInput,
		) (pluginapi.RewriteQueryOutput, error) {
			sawTenant = call.TenantID
			return pluginapi.RewriteQueryOutput{Query: in.RewrittenQuery + " (acme)"}, nil
		},
		FilterResults: func(_ context.Context, _ *pluginsdk.Call,
			in pluginapi.FilterResultsInput,
		) (pluginapi.FilterResultsOutput, error) {
			// Drop confidential passages, put the rest in reverse, and try
			// to sneak in one that was not retrieved.
			keep := []string{"forged"}
			for i := len(in.Results) - 1; i >= 0; i-- {
				if !strings.Contains(in.Results[i].Content, "confidential") {
					keep = append(keep, in.Results[i].ID, in.Results[i].ID)
				}
			}
			return pluginapi.FilterResultsOutput{Keep: keep}, nil
		},
		Answer: func(_ context.Context, _ *pluginsdk.Call, in pluginapi.AnswerInput) (pluginapi.AnswerOutput, error) {
			if strings.Contains(in.Answer, "slow") {
				time.Sleep(hookTimeout + time.Second)
			}
			return pluginapi.AnswerOutput{Append: "_Checked by ACME Guard._"}, nil
		},
	})
	srv := httptest.NewServer(plugin.Handler())
	defer srv.Close()
	p, err := pkg.Open(plugintest.Zip(t, map[string]string{"plugin.yaml": guardManifest, "bin/guard": "x"}))
	if err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	if err := reg.Register(p.Manifest); err != nil {
		t.Fatal(err)
	}
	h := NewPipelineHooks(NewInvoker(fakeClients{client.New(srv.URL, nil, nil)}), reg, enabledFor{7: true})
	ctx := context.Background()
	cm := &types.ChatManage{}
	cm.TenantID, cm.Query = 7, "what is the price"

	if got := h.RewriteQuery(ctx, cm, "price of product"); got != "price of product (acme)" || sawTenant != 7 {
		t.Fatalf("rewrite = %q (tenant %d)", got, sawTenant)
	}
	results := []*types.SearchResult{
		{ID: "a", Content: "public price list"},
		{ID: "b", Content: "confidential margin"},
		{ID: "c", Content: "public FAQ"},
	}
	got := h.FilterResults(ctx, cm, results)
	if len(got) != 2 || got[0].ID != "c" || got[1].ID != "a" {
		t.Fatalf("filtered = %v", got)
	}
	if extra := h.AnswerAppendix(ctx, cm, "It costs 10."); extra != "_Checked by ACME Guard._" {
		t.Fatalf("appendix = %q", extra)
	}
	if extra := h.AnswerAppendix(ctx, cm, "slow answer"); extra != "" {
		t.Fatalf("a hook past its time still answered: %q", extra)
	}

	off := &types.ChatManage{}
	off.TenantID = 8
	if got := h.RewriteQuery(ctx, off, "q"); got != "q" {
		t.Fatalf("a workspace with the plugin off was hooked: %q", got)
	}
}
