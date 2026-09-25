package datasource

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/plugin/configschema"
	"github.com/Tencent/WeKnora/internal/types"
)

// implementedConnectors are the types container.initConnectorRegistry
// registers; each needs a credential form.
var implementedConnectors = []string{
	types.ConnectorTypeFeishu, types.ConnectorTypeLark, types.ConnectorTypeFeishuDrive,
	types.ConnectorTypeLarkDrive, types.ConnectorTypeNotion, types.ConnectorTypeConfluence,
	types.ConnectorTypeYuque, types.ConnectorTypeDingTalk, types.ConnectorTypeIMA,
	types.ConnectorTypeRSS, types.ConnectorTypeGitLab,
}

func TestEveryImplementedConnectorHasAValidForm(t *testing.T) {
	for _, typ := range implementedConnectors {
		if _, ok := ConnectorMetadataRegistry[typ]; !ok {
			t.Errorf("%s: no metadata", typ)
			continue
		}
		meta := withForm(ConnectorMetadataRegistry[typ])
		if meta.ConfigSchema == nil {
			t.Errorf("%s: no config schema", typ)
			continue
		}
		if err := meta.ConfigSchema.Check(); err != nil {
			t.Errorf("%s: invalid schema: %v", typ, err)
		}
	}
}

func TestConfluenceSchemaSwitchesSecretByEdition(t *testing.T) {
	s := confluenceSchema()
	base := map[string]any{"base_url": "https://c.example.com", "username": "bob"}

	server := map[string]any{"edition": "server", "base_url": base["base_url"], "username": "bob"}
	errs := configschema.Validate(s, server)
	if len(errs) != 1 || errs[0].Path != "password" {
		t.Fatalf("server edition must require the password only, got %v", errs)
	}
	cloud := map[string]any{"edition": "cloud", "base_url": base["base_url"], "username": "bob"}
	errs = configschema.Validate(s, cloud)
	if len(errs) != 1 || errs[0].Path != "api_token" {
		t.Fatalf("cloud edition must require the API token only, got %v", errs)
	}
	if got := s.SecretPaths(); strings.Join(got, ",") != "api_token,password" {
		t.Fatalf("secrets = %v", got)
	}
}

// fakeConnector is enough to register a type.
type fakeConnector struct {
	Connector
	typ string
}

func (f fakeConnector) Type() string { return f.typ }

func (fakeConnector) Validate(context.Context, *types.DataSourceConfig) error { return nil }

func TestRegistryMetadataListsRegisteredOnly(t *testing.T) {
	r := NewConnectorRegistry()
	for _, typ := range []string{types.ConnectorTypeNotion, types.ConnectorTypeFeishu, "zz-custom"} {
		if err := r.Register(fakeConnector{typ: typ}); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for _, m := range r.Metadata() {
		got = append(got, m.Type)
	}
	if strings.Join(got, ",") != "feishu,notion,zz-custom" {
		t.Fatalf("Metadata order = %v", got)
	}
	meta := r.Metadata()
	if meta[0].ConfigSchema == nil || len(meta[0].RequiredPermissions) == 0 || meta[0].DocURL == "" {
		t.Fatalf("feishu form missing: %+v", meta[0])
	}
	if meta[2].Name != "zz-custom" || meta[2].ConfigSchema != nil {
		t.Fatalf("unknown connector should fall back to its type: %+v", meta[2])
	}
}

func TestListAvailableConnectorsIsStable(t *testing.T) {
	first := ListAvailableConnectors()
	for range 5 {
		again := ListAvailableConnectors()
		for i := range first {
			if first[i].Type != again[i].Type {
				t.Fatalf("order changed at %d: %s vs %s", i, first[i].Type, again[i].Type)
			}
		}
	}
}
