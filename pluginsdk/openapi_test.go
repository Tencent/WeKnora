package pluginsdk

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The OpenAPI description and the SDK's routes must list the same endpoints,
// so a protocol change cannot land in one and not the other.
func TestOpenAPIMatchesRoutes(t *testing.T) {
	raw, err := os.ReadFile("pluginapi/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// Windows checkouts may carry CRLF line endings.
	spec := strings.ReplaceAll(string(raw), "\r\n", "\n")
	var documented []string
	for _, m := range regexp.MustCompile(`(?m)^  (/v1/[^:]+):$`).FindAllStringSubmatch(spec, -1) {
		documented = append(documented, m[1])
	}
	src, err := os.ReadFile("plugin.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"websearch.go", "connector.go", "parser.go", "ui.go", "events.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src = append(src, b...)
	}
	var routed []string
	route := regexp.MustCompile(`HandleFunc\(\s*"(?:GET|POST) (/v1/[^"]+)"`)
	for _, m := range route.FindAllStringSubmatch(string(src), -1) {
		routed = append(routed, m[1])
	}
	sort.Strings(documented)
	sort.Strings(routed)
	if strings.Join(documented, "\n") != strings.Join(routed, "\n") {
		t.Fatalf(
			"openapi.yaml documents:\n%s\n\nthe SDK routes:\n%s",
			strings.Join(documented, "\n"),
			strings.Join(routed, "\n"),
		)
	}
}
