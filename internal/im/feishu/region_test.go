package feishu

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/im"
)

// The two regions must stay distinct in every dimension that reaches the
// network or the session store: sending a Lark request to open.feishu.cn fails
// authentication, and reporting the wrong platform merges the two clouds'
// sessions together.
func TestRegions_AreDistinct(t *testing.T) {
	if RegionFeishu.OpenBaseURL == RegionLark.OpenBaseURL {
		t.Errorf("regions share OpenBaseURL %q", RegionFeishu.OpenBaseURL)
	}
	if RegionFeishu.Platform == RegionLark.Platform {
		t.Errorf("regions share Platform %q", RegionFeishu.Platform)
	}
	if RegionFeishu.Platform != im.PlatformFeishu {
		t.Errorf("RegionFeishu.Platform = %q, want %q", RegionFeishu.Platform, im.PlatformFeishu)
	}
	if RegionLark.Platform != im.PlatformLark {
		t.Errorf("RegionLark.Platform = %q, want %q", RegionLark.Platform, im.PlatformLark)
	}
}

func TestRegions_BaseURLHosts(t *testing.T) {
	cases := []struct {
		region Region
		want   string
	}{
		{RegionFeishu, "https://open.feishu.cn"},
		{RegionLark, "https://open.larksuite.com"},
	}
	for _, c := range cases {
		if c.region.OpenBaseURL != c.want {
			t.Errorf("%s OpenBaseURL = %q, want %q", c.region.Label, c.region.OpenBaseURL, c.want)
		}
		if strings.HasSuffix(c.region.OpenBaseURL, "/") {
			t.Errorf("%s OpenBaseURL has a trailing slash: %q", c.region.Label, c.region.OpenBaseURL)
		}
	}
}

// Every API call must land on the region's own cloud.
func TestAdapterAPI_UsesRegionHost(t *testing.T) {
	cases := []struct {
		region     Region
		wantPrefix string
	}{
		{RegionFeishu, "https://open.feishu.cn/open-apis/"},
		{RegionLark, "https://open.larksuite.com/open-apis/"},
	}
	for _, c := range cases {
		a := NewAdapter(c.region, "cli_app", "secret", "", "")

		got := a.api("/open-apis/im/v1/messages/%s/reply", "om_1")
		want := c.wantPrefix + "im/v1/messages/om_1/reply"
		if got != want {
			t.Errorf("%s api() = %q, want %q", c.region.Label, got, want)
		}

		// A path with no format verbs must survive unchanged.
		if got := a.api("/open-apis/cardkit/v1/cards"); got != c.wantPrefix+"cardkit/v1/cards" {
			t.Errorf("%s api() = %q, want %q", c.region.Label, got, c.wantPrefix+"cardkit/v1/cards")
		}
	}
}

func TestAdapterPlatform_FollowsRegion(t *testing.T) {
	if got := NewAdapter(RegionFeishu, "a", "b", "", "").Platform(); got != im.PlatformFeishu {
		t.Errorf("Feishu adapter Platform() = %q, want %q", got, im.PlatformFeishu)
	}
	if got := NewAdapter(RegionLark, "a", "b", "", "").Platform(); got != im.PlatformLark {
		t.Errorf("Lark adapter Platform() = %q, want %q", got, im.PlatformLark)
	}
}

// The streaming card placeholder follows the region so Lark users are not shown
// Chinese copy.
func TestBuildStreamingCardJSON_PlaceholderFollowsRegion(t *testing.T) {
	for _, region := range []Region{RegionFeishu, RegionLark} {
		raw := buildStreamingCardJSON(region)

		var card struct {
			Config struct {
				StreamingMode bool `json:"streaming_mode"`
				Summary       struct {
					Content string `json:"content"`
				} `json:"summary"`
			} `json:"config"`
			Body struct {
				Elements []struct {
					Content   string `json:"content"`
					ElementID string `json:"element_id"`
				} `json:"elements"`
			} `json:"body"`
		}
		if err := json.Unmarshal([]byte(raw), &card); err != nil {
			t.Fatalf("%s card is not valid JSON: %v", region.Label, err)
		}

		if !card.Config.StreamingMode {
			t.Errorf("%s card has streaming_mode disabled", region.Label)
		}
		if card.Config.Summary.Content != region.ThinkingText {
			t.Errorf("%s summary = %q, want %q", region.Label, card.Config.Summary.Content, region.ThinkingText)
		}
		if len(card.Body.Elements) != 1 {
			t.Fatalf("%s card has %d elements, want 1", region.Label, len(card.Body.Elements))
		}
		el := card.Body.Elements[0]
		if el.ElementID != streamingElementID {
			t.Errorf("%s element_id = %q, want %q", region.Label, el.ElementID, streamingElementID)
		}
		if !strings.Contains(el.Content, region.ThinkingText) {
			t.Errorf("%s element content %q does not contain %q", region.Label, el.Content, region.ThinkingText)
		}
	}

	// Guard the point of the whole exercise: the copy actually differs.
	if RegionFeishu.ThinkingText == RegionLark.ThinkingText {
		t.Error("Feishu and Lark share ThinkingText; Lark users would see Chinese copy")
	}
}
