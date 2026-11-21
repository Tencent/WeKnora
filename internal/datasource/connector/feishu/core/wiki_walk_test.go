package core

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func wikiNodeTokens(nodes []WikiNode) []string {
	tokens := make([]string, 0, len(nodes))
	for _, node := range nodes {
		tokens = append(tokens, node.NodeToken)
	}
	return tokens
}

func TestWikiDirectoryConcurrencyAndOrder(t *testing.T) {
	t.Setenv("FEISHU_API_REQUESTS_PER_MINUTE", "600000")
	const count = wikiDirectoryConcurrency + 2
	var roots []WikiNode
	var want []string
	for i := range count {
		id := fmt.Sprintf("dir-%d", i)
		roots = append(roots, WikiNode{NodeToken: id, HasChild: true})
		want = append(want, id, id+"-first", id+"-second")
	}
	arrived := make(chan struct{}, count*2)
	release := make(chan struct{})
	resume := sync.OnceFunc(func() { close(release) })
	var active, peak atomic.Int32
	ts, cfg := retryTestServer(t, "/open-apis/wiki/v2/spaces/root/nodes", func(w http.ResponseWriter, r *http.Request) {
		parent := r.URL.Query().Get("parent_node_token")
		if parent == "" {
			writeJSON(w, WikiNodeListResponse{Data: WikiNodeListData{Items: roots}})
			return
		}
		current := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); current > old; old = peak.Load() {
			if peak.CompareAndSwap(old, current) {
				break
			}
		}
		arrived <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		page := WikiNodeListData{Items: []WikiNode{{NodeToken: parent + "-second"}}}
		if r.URL.Query().Get("page_token") == "" {
			page = WikiNodeListData{
				Items: []WikiNode{{NodeToken: parent + "-first"}}, HasMore: true, PageToken: "next",
			}
		}
		writeJSON(w, WikiNodeListResponse{Data: page})
	})
	defer ts.Close()
	defer resume()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	type result struct {
		nodes []WikiNode
		err   error
	}
	done := make(chan result, 1)
	go func() {
		nodes, err := NewClient(cfg).ListWikiNodesRecursiveFrom(ctx, "root", "")
		done <- result{nodes, err}
	}()
	for i := range wikiDirectoryConcurrency {
		select {
		case <-arrived:
		case <-ctx.Done():
			t.Fatalf("only %d sibling requests reached the barrier", i)
		}
	}
	resume()
	var got result
	select {
	case got = <-done:
	case <-ctx.Done():
		t.Fatal("directory walk did not finish")
	}
	require.NoError(t, got.err)
	require.Equal(t, int32(wikiDirectoryConcurrency), peak.Load())
	require.Equal(t, want, wikiNodeTokens(got.nodes))
}

func TestWikiDirectoryPartialFailureAndCycles(t *testing.T) {
	t.Setenv("FEISHU_API_REQUESTS_PER_MINUTE", "600000")
	var invalidRequests atomic.Int32
	ts, cfg := retryTestServer(t, "/open-apis/wiki/v2/spaces/root/nodes", func(w http.ResponseWriter, r *http.Request) {
		var nodes []WikiNode
		switch r.URL.Query().Get("parent_node_token") {
		case "":
			nodes = []WikiNode{
				{NodeToken: "denied", HasChild: true},
				{NodeToken: "healthy", HasChild: true},
				{NodeToken: "cycle", HasChild: true},
				{HasChild: true},
			}
		case "denied":
			w.WriteHeader(http.StatusForbidden)
			return
		case "healthy":
			nodes = []WikiNode{{NodeToken: "document"}}
		case "cycle":
			nodes = []WikiNode{{NodeToken: "cycle", HasChild: true}}
		default:
			invalidRequests.Add(1)
		}
		writeJSON(w, WikiNodeListResponse{Data: WikiNodeListData{Items: nodes}})
	})
	defer ts.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	nodes, err := NewClient(cfg).ListWikiNodesRecursiveFrom(ctx, "root", "")
	var partial *PartialWikiNodeListError
	require.ErrorAs(t, err, &partial)
	require.Equal(t, []string{"denied", "healthy", "document", "cycle", "cycle", ""}, wikiNodeTokens(nodes))
	require.Len(t, partial.Failures, 3)
	require.Equal(t, "denied", partial.Failures[0].Node.NodeToken)
	require.Contains(t, partial.Failures[1].Err.Error(), "cyclic")
	require.Empty(t, partial.Failures[2].Node.NodeToken)
	require.Zero(t, invalidRequests.Load())
}

func TestWikiDirectoryCancellationStopsWorkers(t *testing.T) {
	t.Setenv("FEISHU_API_REQUESTS_PER_MINUTE", "600000")
	started := make(chan struct{}, wikiDirectoryConcurrency)
	stopped := make(chan struct{}, wikiDirectoryConcurrency)
	ts, cfg := retryTestServer(t, "/open-apis/wiki/v2/spaces/root/nodes", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("parent_node_token") == "" {
			var nodes []WikiNode
			for i := range wikiDirectoryConcurrency + 1 {
				nodes = append(nodes, WikiNode{NodeToken: fmt.Sprint(i), HasChild: true})
			}
			writeJSON(w, WikiNodeListResponse{Data: WikiNodeListData{Items: nodes}})
			return
		}
		started <- struct{}{}
		<-r.Context().Done()
		stopped <- struct{}{}
	})
	defer ts.Close()
	timeout, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	ctx, cancel := context.WithCancel(timeout)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := NewClient(cfg).ListWikiNodesRecursiveFrom(ctx, "root", "")
		done <- err
	}()
	for range wikiDirectoryConcurrency {
		select {
		case <-started:
		case <-timeout.Done():
			t.Fatal("requests did not start")
		}
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-timeout.Done():
		t.Fatal("walk did not return after cancellation")
	}
	for range wikiDirectoryConcurrency {
		select {
		case <-stopped:
		case <-timeout.Done():
			t.Fatal("request survived cancellation")
		}
	}
}

func TestWikiDirectorySelectedRootAndRootFailure(t *testing.T) {
	t.Setenv("FEISHU_API_REQUESTS_PER_MINUTE", "600000")
	ts, cfg := retryTestServer(t, "/open-apis/wiki/v2/spaces/root/nodes", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("parent_node_token") != "selected" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		writeJSON(w, WikiNodeListResponse{Data: WikiNodeListData{Items: []WikiNode{{NodeToken: "leaf"}}}})
	})
	defer ts.Close()
	ts.Config.Handler.(*http.ServeMux).HandleFunc("/open-apis/wiki/v2/spaces/get_node",
		func(w http.ResponseWriter, r *http.Request) {
			node := WikiNode{NodeToken: r.URL.Query().Get("token"), HasChild: true}
			writeJSON(w, WikiNodeInfoResponse{Data: WikiNodeInfoData{Node: node}})
		})
	client := NewClient(cfg)
	nodes, err := client.ListWikiNodesRecursiveFrom(t.Context(), "root", "selected")
	require.NoError(t, err)
	require.Equal(t, []string{"selected", "leaf"}, wikiNodeTokens(nodes))
	nodes, err = client.ListWikiNodesRecursiveFrom(t.Context(), "root", "denied")
	var partial *PartialWikiNodeListError
	require.ErrorAs(t, err, &partial)
	require.Equal(t, []string{"denied"}, wikiNodeTokens(nodes))
	require.Len(t, partial.Failures, 1)
	nodes, err = client.ListWikiNodesRecursiveFrom(t.Context(), "root", "")
	require.Error(t, err)
	require.NotErrorAs(t, err, &partial)
	require.Empty(t, nodes)
}
