package datasource

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestWeComChatArchiveMetadataRegistered(t *testing.T) {
	meta, ok := ConnectorMetadataRegistry[types.ConnectorTypeWeComChatArchive]
	if !ok {
		t.Fatalf("metadata for %q is not registered", types.ConnectorTypeWeComChatArchive)
	}
	if meta.Type != types.ConnectorTypeWeComChatArchive {
		t.Fatalf("Type = %q, want %q", meta.Type, types.ConnectorTypeWeComChatArchive)
	}
	if meta.Name != "企业微信会话存档" {
		t.Fatalf("Name = %q", meta.Name)
	}
	if meta.AuthType != "api_key" {
		t.Fatalf("AuthType = %q, want api_key", meta.AuthType)
	}
	if strings.Join(meta.Capabilities, ",") != "incremental" {
		t.Fatalf("Capabilities = %v, want [incremental]", meta.Capabilities)
	}
}

func TestFeishuMetadataDoesNotAdvertiseWebhook(t *testing.T) {
	meta := ConnectorMetadataRegistry[types.ConnectorTypeFeishu]

	for _, capability := range meta.Capabilities {
		if capability == "webhook" {
			t.Fatalf("Feishu connector should not advertise webhook until webhook sync is implemented")
		}
	}
}
