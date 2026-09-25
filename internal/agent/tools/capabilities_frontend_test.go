package tools

import (
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// frontendCapabilitiesFile is the frontend copy of ToolCapabilityRequirements.
// The agent editor evaluates it synchronously in many places, so it stays a
// copy; this test keeps the two from drifting.
const frontendCapabilitiesFile = "../../../frontend/src/utils/tool-capabilities.ts"

func parseFrontendRequirements(t *testing.T) map[string]ToolRequirement {
	t.Helper()
	data, err := os.ReadFile(frontendCapabilitiesFile)
	if err != nil {
		t.Fatalf("read %s: %v", frontendCapabilitiesFile, err)
	}
	src := string(data)
	start := strings.Index(src, "TOOL_CAPABILITY_REQUIREMENTS")
	end := strings.Index(src[start:], "\n};")
	if start < 0 || end < 0 {
		t.Fatal("TOOL_CAPABILITY_REQUIREMENTS not found in the frontend file")
	}
	body := src[start : start+end]

	entry := regexp.MustCompile(`(?m)^\s*([a-z_]+):\s*\{([^}]*)\},?`)
	list := func(fields, key string) []KBCapability {
		m := regexp.MustCompile(key + `:\s*\[([^\]]*)\]`).FindStringSubmatch(fields)
		if m == nil {
			return nil
		}
		var out []KBCapability
		for _, v := range regexp.MustCompile(`'([a-z]+)'`).FindAllStringSubmatch(m[1], -1) {
			out = append(out, KBCapability(v[1]))
		}
		return out
	}
	out := map[string]ToolRequirement{}
	for _, m := range entry.FindAllStringSubmatch(body, -1) {
		fields := m[2]
		out[m[1]] = ToolRequirement{
			AnyOf:         list(fields, "anyOf"),
			AllOf:         list(fields, "allOf"),
			ConsumesFiles: strings.Contains(fields, "consumesFiles: true"),
			Auxiliary:     strings.Contains(fields, "auxiliary: true"),
		}
	}
	return out
}

func sortedCaps(c []KBCapability) []KBCapability {
	out := slices.Clone(c)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func TestFrontendToolCapabilitiesMatchBackend(t *testing.T) {
	front := parseFrontendRequirements(t)
	if len(front) == 0 {
		t.Fatal("parsed no frontend requirements")
	}
	for name, back := range ToolCapabilityRequirements {
		f, ok := front[name]
		if !ok {
			t.Errorf("%s: missing from %s", name, frontendCapabilitiesFile)
			continue
		}
		if !slices.Equal(sortedCaps(f.AnyOf), sortedCaps(back.AnyOf)) ||
			!slices.Equal(sortedCaps(f.AllOf), sortedCaps(back.AllOf)) ||
			f.ConsumesFiles != back.ConsumesFiles || f.Auxiliary != back.Auxiliary {
			t.Errorf("%s: frontend %+v, backend %+v", name, f, back)
		}
	}
	for name := range front {
		if _, ok := ToolCapabilityRequirements[name]; !ok {
			t.Errorf("%s: only in the frontend map", name)
		}
	}
}
