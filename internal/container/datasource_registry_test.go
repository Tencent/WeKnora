package container

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestConnectorRegistryIncludesDingTalk(t *testing.T) {
	registry, err := initConnectorRegistry()
	if err != nil {
		t.Fatal(err)
	}
	connector, err := registry.Get(types.ConnectorTypeDingTalk)
	if err != nil {
		t.Fatal(err)
	}
	if connector.Type() != types.ConnectorTypeDingTalk {
		t.Fatalf("registered connector type = %q", connector.Type())
	}
}
