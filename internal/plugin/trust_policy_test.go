package plugin

import "testing"

func TestPluginTrustConfigDefaultsOffline(t *testing.T) {
	t.Setenv("WEKNORA_PLUGIN_TRUST_LEVELS", "")
	if got := LoadPluginTrustConfig().Level("example"); got != TrustOffline {
		t.Fatalf("got %q", got)
	}
}

func TestPluginTrustConfigReadsByPluginID(t *testing.T) {
	t.Setenv("WEKNORA_PLUGIN_TRUST_LEVELS", `{"dingtalk":"trusted","localdir":"offline","third":"isolated"}`)
	c := LoadPluginTrustConfig()
	if c.Level("dingtalk") != TrustTrusted || c.Level("localdir") != TrustOffline || c.Level("third") != TrustIsolated {
		t.Fatalf("unexpected config: %#v", c)
	}
}

func TestTrustedPlanUsesManifestDestinations(t *testing.T) {
	m := Manifest{ID: "dingtalk", Entrypoint: "./plugin", Permissions: Permissions{Network: NetworkAllowlist, AllowedDestinations: []string{"api.dingtalk.com:443"}}}
	plan, err := ResolveExecutionPlan(m, TrustTrusted)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Network != NetworkAllowlist || len(plan.Allowlist) != 1 || plan.Allowlist[0] != "api.dingtalk.com:443" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

func TestManifestRejectsEgressNetworkPolicy(t *testing.T) {
	m := Manifest{
		APIVersion:      APIVersionV1,
		ID:              "localdir",
		Name:            "LocalDir",
		Version:         "1.0.0",
		ExtensionType:   ExtensionDataSource,
		ProtocolVersion: ProtocolVersionV1,
		Entrypoint:      "docker://localdir:1",
		Permissions:     Permissions{Network: NetworkPolicy("egress")},
	}
	if err := m.Validate("0.7.2"); err == nil {
		t.Fatalf("expected manifest with 'egress' network policy to be rejected")
	}
}

func TestIsolatedTrustUsesAllowlist(t *testing.T) {
	m := Manifest{ID: "localdir", Entrypoint: "docker://localdir:1", Permissions: Permissions{Network: NetworkAllowlist, AllowedDestinations: []string{"api.example.com:443"}}}
	plan, err := ResolveExecutionPlan(m, TrustIsolated)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Isolation != IsolationOCI || plan.Network != NetworkAllowlist ||
		len(plan.Allowlist) != 1 || plan.Allowlist[0] != "api.example.com:443" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}
