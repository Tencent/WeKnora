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

func TestDingTalkMetadataAdvertisesImplementedCapabilities(t *testing.T) {
	meta := ConnectorMetadataRegistry[types.ConnectorTypeDingTalk]
	if meta.AuthType != "oauth2" {
		t.Fatalf("DingTalk AuthType = %q", meta.AuthType)
	}
	want := map[string]bool{"incremental": true, "deletion_sync": true}
	for _, capability := range meta.Capabilities {
		delete(want, capability)
	}
	if len(want) != 0 {
		t.Fatalf("DingTalk metadata is missing capabilities: %v", want)
	}
}
