package langfuse

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMergeMetadataHeader(t *testing.T) {
	tests := []struct {
		name     string
		initial  map[string]interface{}
		header   string
		wantKeys map[string]interface{}
	}{
		{
			name:     "empty header keeps builtin fields",
			initial:  map[string]interface{}{"http.method": "POST"},
			header:   "",
			wantKeys: map[string]interface{}{"http.method": "POST"},
		},
		{
			name:     "whitespace header is ignored",
			initial:  map[string]interface{}{"http.path": "/api/v1/knowledge-chat/:id"},
			header:   "   ",
			wantKeys: map[string]interface{}{"http.path": "/api/v1/knowledge-chat/:id"},
		},
		{
			name:     "merges caller json",
			initial:  map[string]interface{}{"http.method": "POST"},
			header:   `{"ticket_id":"T-123","biz_line":"ops"}`,
			wantKeys: map[string]interface{}{"http.method": "POST", "ticket_id": "T-123", "biz_line": "ops"},
		},
		{
			name:     "malformed json is ignored",
			initial:  map[string]interface{}{"http.method": "POST"},
			header:   `{not-json`,
			wantKeys: map[string]interface{}{"http.method": "POST"},
		},
		{
			name:     "non-object json is ignored",
			initial:  map[string]interface{}{},
			header:   `["a","b"]`,
			wantKeys: map[string]interface{}{},
		},
		{
			name:     "builtin fields always win on conflict",
			initial:  map[string]interface{}{"http.path": "/api/v1/chat"},
			header:   `{"http.path":"/attempted/override","biz":"x"}`,
			wantKeys: map[string]interface{}{"http.path": "/api/v1/chat", "biz": "x"},
		},
		{
			name:     "numeric and boolean values pass through",
			initial:  map[string]interface{}{},
			header:   `{"retry":3,"force":true,"score":0.5}`,
			wantKeys: map[string]interface{}{"retry": float64(3), "force": true, "score": 0.5},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.initial
			mergeMetadataHeader(got, tt.header)
			if len(got) != len(tt.wantKeys) {
				t.Fatalf("key count = %d, want %d (%v)", len(got), len(tt.wantKeys), got)
			}
			for k, want := range tt.wantKeys {
				gotV, ok := got[k]
				if !ok {
					t.Fatalf("missing key %q in %v", k, got)
				}
				gb, _ := json.Marshal(gotV)
				wb, _ := json.Marshal(want)
				if string(gb) != string(wb) {
					t.Fatalf("key %q = %s, want %s", k, gb, wb)
				}
			}
		})
	}
}

func TestGinMiddlewareMergesMetadataHeader(t *testing.T) {
	_, exp := newTestManager(t)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(GinMiddleware())
	var observed map[string]interface{}
	r.POST("/api/v1/knowledge-chat/:id", func(c *gin.Context) {
		if tr, ok := TraceFromContext(c.Request.Context()); ok {
			observed = tr.metadata
		}
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/knowledge-chat/sess-1", nil)
	req.Header.Set("X-Langfuse-Metadata", `{"ticket_id":"T-99","priority":"P1"}`)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if observed["ticket_id"] != "T-99" || observed["priority"] != "P1" {
		t.Fatalf("metadata header not merged into trace: %v", observed)
	}
	// Builtin correlation fields must survive.
	if observed["http.method"] != "POST" || observed["http.path"] != "/api/v1/knowledge-chat/:id" {
		t.Fatalf("builtin fields corrupted: %v", observed)
	}

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no span exported")
	}
	spanMeta := spanAttr(spans[len(spans)-1].Attributes, attrTraceMetadata)
	if spanMeta == "" {
		t.Fatal("langfuse.trace.metadata attribute missing")
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(spanMeta), &got); err != nil {
		t.Fatalf("metadata attribute not valid json: %v", err)
	}
	if got["ticket_id"] != "T-99" {
		t.Fatalf("ticket_id not exported in trace metadata: %v", got)
	}
}

func TestGinMiddlewareIgnoresMalformedMetadataHeader(t *testing.T) {
	_, exp := newTestManager(t)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(GinMiddleware())
	var observed map[string]interface{}
	r.POST("/api/v1/agent-chat/:id", func(c *gin.Context) {
		if tr, ok := TraceFromContext(c.Request.Context()); ok {
			observed = tr.metadata
		}
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent-chat/sess-1", nil)
	req.Header.Set("X-Langfuse-Metadata", `{broken json`)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("request failed on malformed header, status = %d", w.Code)
	}
	if observed["http.method"] != "POST" {
		t.Fatalf("malformed header corrupted or blocked trace: %v", observed)
	}
	// No caller key should be injected.
	if _, ok := observed["ticket_id"]; ok {
		t.Fatalf("malformed header unexpectedly injected keys: %v", observed)
	}

	if len(exp.GetSpans()) == 0 {
		t.Fatal("no span exported despite invalid header (request must still be traced)")
	}
}
