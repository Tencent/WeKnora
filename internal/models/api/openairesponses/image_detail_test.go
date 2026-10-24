package openairesponses

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
)

// Same contract as the completions path (#3451): no detail unless the caller
// chose one — no hardcoded "auto", no silent defaulting on the passthrough.
func TestImagesFallbackOmitsDetail(t *testing.T) {
	c := newTestClient(t, nil, false)
	body, err := c.BuildRequestBody([]api.Message{{
		Role: "user", Content: "what is this?", Images: []string{"https://example.com/a.png"},
	}}, nil, false)
	if err != nil {
		t.Fatalf("BuildRequestBody: %v", err)
	}
	roundTripped := roundTrip(t, body)
	data := mustJSON(t, roundTripped)
	if !contains(data, `"image_url":"https://example.com/a.png"`) &&
		!contains(data, `"image_url":"`) {
		t.Fatalf("image part missing on the wire: %s", data)
	}
	if contains(data, `"detail"`) {
		t.Fatalf("hardcoded detail leaked into the wire request: %s", data)
	}
}

func TestMultiContentDetailPassthroughAndOmission(t *testing.T) {
	c := newTestClient(t, nil, false)
	body, err := c.BuildRequestBody([]api.Message{{
		Role: "user",
		MultiContent: []api.MessageContentPart{
			{Type: "image_url", ImageURL: &api.ImageURL{URL: "https://example.com/a.png", Detail: "high"}},
			{Type: "image_url", ImageURL: &api.ImageURL{URL: "https://example.com/b.png"}},
		},
	}}, nil, false)
	if err != nil {
		t.Fatalf("BuildRequestBody: %v", err)
	}
	data := mustJSON(t, roundTrip(t, body))
	if !contains(data, `"detail":"high"`) {
		t.Fatalf("caller-chosen detail not preserved: %s", data)
	}
	if contains(data, `"detail":"auto"`) {
		t.Fatalf("silent default to auto reappeared: %s", data)
	}
}
