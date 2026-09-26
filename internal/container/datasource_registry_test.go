package container

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestConnectorRegistryIncludesDingTalk(t *testing.T) {
	registry, err := initConnectorRegistry()
	if err != nil {
		t.Fatalf("initConnectorRegistry() error = %v", err)
	}
	connector, err := registry.Get(types.ConnectorTypeDingTalk)
	if err != nil {
		t.Fatalf("DingTalk connector is not registered: %v", err)
	}
	if connector.Type() != types.ConnectorTypeDingTalk {
		t.Fatalf("connector.Type() = %q", connector.Type())
	}
}

func TestConnectorRegistryIncludesPaperless(t *testing.T) {
	registry, err := initConnectorRegistry()
	if err != nil {
		t.Fatalf("initConnectorRegistry() error = %v", err)
	}
	connector, err := registry.Get(types.ConnectorTypePaperless)
	if err != nil {
		t.Fatalf("Paperless connector is not registered: %v", err)
	}
	if connector.Type() != types.ConnectorTypePaperless {
		t.Fatalf("connector.Type() = %q", connector.Type())
	}
}
