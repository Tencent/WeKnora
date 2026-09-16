package confluence

import (
	"net/url"
	"testing"
)

func TestResolveEndpointKeepsCloudContextPath(t *testing.T) {
	client := &client{cfg: config{baseURL: "https://team.atlassian.net/wiki"}}
	for _, endpoint := range []string{
		"/api/v2/spaces?cursor=one",
		"/wiki/api/v2/spaces?cursor=two",
		"https://team.atlassian.net/wiki/api/v2/spaces?cursor=three",
	} {
		got, err := client.resolveEndpoint(endpoint)
		if err != nil {
			t.Fatalf("resolveEndpoint(%q): %v", endpoint, err)
		}
		if got == "https://team.atlassian.net/wiki/wiki/api/v2/spaces?cursor=two" {
			t.Fatalf("resolveEndpoint(%q) duplicated /wiki: %s", endpoint, got)
		}
	}
}

func TestResolveEndpointRejectsForeignOrigin(t *testing.T) {
	client := &client{cfg: config{baseURL: "https://team.atlassian.net/wiki"}}
	if _, err := client.resolveEndpoint("https://attacker.example/api/v2/spaces"); err == nil {
		t.Fatal("resolveEndpoint accepted a foreign pagination origin")
	}
}

func TestServerPageSearchQuotesSpecialSpaceKey(t *testing.T) {
	endpoint := serverPageSearchEndpoint("~personal_space")
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}

	want := `space="~personal_space" AND type=page AND status=current`
	if got := parsed.Query().Get("cql"); got != want {
		t.Fatalf("CQL = %q, want %q", got, want)
	}
}
