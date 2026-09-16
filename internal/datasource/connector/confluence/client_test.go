package confluence

import (
	"net/url"
	"strings"
	"testing"
	"time"
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

func TestServerSpacePagesEndpointEscapesPersonalKey(t *testing.T) {
	endpoint := serverSpacePagesEndpoint("~personal_space")
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}
	if parsed.Path != "/rest/api/space/~personal_space/content/page" {
		t.Fatalf("path = %q", parsed.Path)
	}
	if parsed.Query().Get("expand") != "version,space" {
		t.Fatalf("expand = %q", parsed.Query().Get("expand"))
	}
}

func TestCapRetryDelayBoundsRetryAfter(t *testing.T) {
	if got := capRetryDelay(0); got != 100*time.Millisecond {
		t.Fatalf("capRetryDelay(0) = %v", got)
	}
	if got := capRetryDelay(5 * time.Minute); got != maxRetryDelay {
		t.Fatalf("capRetryDelay(5m) = %v", got)
	}
}

func TestResolveEndpointDoesNotDoubleEncodeSpaceKeys(t *testing.T) {
	client := &client{cfg: config{baseURL: "https://confluence.test"}}
	endpoint := "/rest/api/space/" + url.PathEscape("~foo bar") + "/content/page"
	got, err := client.resolveEndpoint(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "%2520") {
		t.Fatalf("resolveEndpoint double-encoded path: %s", got)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/rest/api/space/~foo bar/content/page" {
		t.Fatalf("path = %q", parsed.Path)
	}
}

func TestWithServerPageExpandRestoresDroppedQuery(t *testing.T) {
	got := withServerPageExpand("/rest/api/space/ENG/content/page?limit=100&start=100")
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("expand") != serverPageExpand || parsed.Query().Get("start") != "100" {
		t.Fatalf("withServerPageExpand() = %s", got)
	}
}

func TestResolveEndpointRejectsContextPathTraversal(t *testing.T) {
	client := &client{cfg: config{baseURL: "https://team.atlassian.net/wiki"}}
	for _, endpoint := range []string{
		"/wiki/../rest/other",
		"https://team.atlassian.net/wiki/../rest/other",
		"../rest/other",
	} {
		if _, err := client.resolveEndpoint(endpoint); err == nil {
			t.Fatalf("resolveEndpoint(%q) accepted a path that leaves /wiki", endpoint)
		}
	}
}

func TestWithCloudPageQueryRestoresDroppedFilters(t *testing.T) {
	got := withCloudPageQuery("/wiki/api/v2/spaces/1/pages?cursor=abc&limit=250")
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("status") != "current" ||
		parsed.Query().Get("depth") != "all" ||
		parsed.Query().Get("cursor") != "abc" {
		t.Fatalf("withCloudPageQuery() = %s", got)
	}
}
