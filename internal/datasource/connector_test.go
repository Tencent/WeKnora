package datasource

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestFeishuMetadataDoesNotAdvertiseWebhook(t *testing.T) {
	meta := ConnectorMetadataRegistry[types.ConnectorTypeFeishu]

	for _, capability := range meta.Capabilities {
		if capability == "webhook" {
			t.Fatalf("Feishu connector should not advertise webhook until webhook sync is implemented")
		}
	}
}

// The Outline connector itself cannot be imported here — package outline
// imports this package, so a test import would be a cycle. Registration is
// covered by initConnectorRegistry failing container startup; what this test
// guards is the metadata entry the UI reads to offer the connector at all.
func TestOutlineMetadataIsRegistered(t *testing.T) {
	meta, ok := ConnectorMetadataRegistry[types.ConnectorTypeOutline]
	if !ok {
		t.Fatal("Outline is missing from ConnectorMetadataRegistry, so the UI will not offer it")
	}
	if meta.Type != types.ConnectorTypeOutline {
		t.Errorf("Type = %q, want %q", meta.Type, types.ConnectorTypeOutline)
	}
	if meta.AuthType != "api_key" {
		t.Errorf("AuthType = %q, want api_key", meta.AuthType)
	}
	// The connector implements revision-based change detection and emits
	// IsDeleted placeholders, so it must advertise both.
	got := map[string]bool{}
	for _, c := range meta.Capabilities {
		got[c] = true
	}
	for _, want := range []string{"incremental", "deletion_sync"} {
		if !got[want] {
			t.Errorf("capability %q not advertised", want)
		}
	}
	// Webhooks are not implemented; advertising one would make the scheduler lie.
	if got["webhook"] {
		t.Error("Outline must not advertise webhook support")
	}
}
