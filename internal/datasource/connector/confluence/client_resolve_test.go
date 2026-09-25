package confluence

import "testing"

func TestResolveEndpointNormalizesCloudPaginationLinks(t *testing.T) {
	client := &client{cfg: config{baseURL: "https://team.atlassian.net/wiki"}}
	cases := map[string]string{
		"/api/v2/spaces?cursor=one":      "https://team.atlassian.net/wiki/api/v2/spaces?cursor=one",
		"/wiki/api/v2/spaces?cursor=two": "https://team.atlassian.net/wiki/api/v2/spaces?cursor=two",
	}
	for endpoint, want := range cases {
		got, err := client.resolveEndpoint(endpoint)
		if err != nil || got != want {
			t.Errorf("resolveEndpoint(%q) = %q, %v; want %q", endpoint, got, err, want)
		}
	}
}
