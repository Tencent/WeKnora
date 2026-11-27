package config

import "testing"

func TestGraphBackendSelection(t *testing.T) {
	for _, tc := range []struct {
		name, explicit, legacy, want string
		enabled, valid               bool
	}{
		{"disabled by default", "", "", GraphBackendNone, false, true},
		{"legacy enabled", "", "true", GraphBackendNeo4j, true, true},
		{"legacy case", "", " TRUE ", GraphBackendNeo4j, true, true},
		{"legacy disabled", "", "false", GraphBackendNone, false, true},
		{"explicit hologres", "hologres", "false", GraphBackendHologres, true, true},
		{"hologres overrides legacy", "hologres", "true", GraphBackendHologres, true, true},
		{"explicit neo4j", "neo4j", "false", GraphBackendNeo4j, true, true},
		{"explicit disable", "none", "true", GraphBackendNone, false, true},
		{"normalize", " HOLOGRES ", "", GraphBackendHologres, true, true},
		{"reject unknown", "typo", "true", "typo", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GRAPH_BACKEND", tc.explicit)
			t.Setenv("NEO4J_ENABLE", tc.legacy)
			if got := GraphBackend(); got != tc.want {
				t.Fatalf("backend = %q, want %q", got, tc.want)
			}
			if got := GraphEnabled(); got != tc.enabled {
				t.Fatalf("enabled = %v, want %v", got, tc.enabled)
			}
			if got := ValidateGraphBackend() == nil; got != tc.valid {
				t.Fatalf("valid = %v, want %v", got, tc.valid)
			}
		})
	}
}
