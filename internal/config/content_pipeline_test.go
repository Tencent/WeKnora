package config

import "testing"

func TestApplyAgentEnvOverridesReadsContentPipelineAuditSecret(t *testing.T) {
	t.Setenv("CONTENT_PIPELINE_AUDIT_SECRET", "server-only-secret")
	cfg := &Config{}

	applyAgentEnvOverrides(cfg)

	if cfg.Agent == nil || cfg.Agent.ContentPipelineAuditSecret != "server-only-secret" {
		t.Fatal("content pipeline audit secret was not loaded into server-only config")
	}
}
