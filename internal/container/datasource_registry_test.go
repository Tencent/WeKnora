package container

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestInitConnectorRegistryRegistersDingTalk(t *testing.T) {
	registry, err := initConnectorRegistry()
	if err != nil {
		t.Fatalf("initConnectorRegistry() error = %v", err)
	}
	if _, err := registry.Get(types.ConnectorTypeDingTalk); err != nil {
		t.Fatalf("DingTalk connector is not registered: %v", err)
	}
}
