package openaicompletions

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
)

// image_url.detail must stay unset unless the caller chose one (#3451):
// OpenAI defaults it server-side, and strict OpenAI-compatible endpoints
// (e.g. MiniMax, "invalid image detail") reject the hardcoded "auto".
func TestImagesFallbackOmitsDetail(t *testing.T) {
	c := newClient(t, nil)
	body := bodyJSON(t, c, []api.Message{{
		Role: "user", Content: "what is this?", Images: []string{"https://example.com/a.png"},
	}}, nil, false)

	data := mustJSON(t, body)
	if want := `"url":"https://example.com/a.png"`; !contains(data, want) {
		t.Fatalf("image part missing on the wire: %s", data)
	}
	if contains(data, `"detail"`) {
		t.Fatalf("hardcoded detail leaked into the wire request: %s", data)
	}
}

func TestMultiContentDetailPassthrough(t *testing.T) {
	c := newClient(t, nil)
	body := bodyJSON(t, c, []api.Message{{
		Role: "user",
		MultiContent: []api.MessageContentPart{
			{Type: "image_url", ImageURL: &api.ImageURL{URL: "https://example.com/a.png", Detail: "low"}},
		},
	}}, nil, false)

	data := mustJSON(t, body)
	if !contains(data, `"detail":"low"`) {
		t.Fatalf("caller-chosen detail not preserved: %s", data)
	}
}
