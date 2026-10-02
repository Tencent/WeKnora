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

func TestPaperlessMetadata(t *testing.T) {
	meta, ok := ConnectorMetadataRegistry[types.ConnectorTypePaperless]
	if !ok {
		t.Fatal("Paperless connector metadata missing")
	}
	if meta.Name != "Paperless-ngx" {
		t.Fatalf("Name = %q", meta.Name)
	}
	if meta.AuthType != "api_key" {
		t.Fatalf("AuthType = %q", meta.AuthType)
	}
	for _, capability := range meta.Capabilities {
		if capability == "incremental" {
			return
		}
	}
	t.Fatalf("Paperless metadata capabilities = %v, want incremental", meta.Capabilities)
}
