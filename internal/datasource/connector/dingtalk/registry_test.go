package dingtalk

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
)

func TestConnectorRegistersInRegistry(t *testing.T) {
	registry := datasource.NewConnectorRegistry()
	if err := registry.Register(NewConnector()); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if _, err := registry.Get(NewConnector().Type()); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
}
