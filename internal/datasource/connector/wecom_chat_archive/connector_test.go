package wecom_chat_archive

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func validConfig() *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type: types.ConnectorTypeWeComChatArchive,
		Credentials: map[string]interface{}{
			"corp_id":             "wwxxxx",
			"secret":              "top-secret",
			"private_key":         "-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----",
			"private_key_version": "1",
		},
		ResourceIDs: []string{"all"},
		Settings: map[string]interface{}{
			"timezone":       "Asia/Shanghai",
			"full_sync_days": float64(30),
		},
	}
}

func TestParseConfigAppliesDefaults(t *testing.T) {
	cfg := validConfig()
	cfg.ResourceIDs = nil
	cfg.Settings = nil

	parsed, err := parseConfig(cfg)
	if err != nil {
		t.Fatalf("parseConfig error: %v", err)
	}
	if parsed.CorpID != "wwxxxx" {
		t.Fatalf("CorpID = %q", parsed.CorpID)
	}
	if parsed.ResourceIDs[0] != "all" {
		t.Fatalf("ResourceIDs = %v", parsed.ResourceIDs)
	}
	if parsed.Settings.Timezone != "Asia/Shanghai" {
		t.Fatalf("Timezone = %q", parsed.Settings.Timezone)
	}
	if parsed.Settings.FullSyncDays != 90 {
		t.Fatalf("FullSyncDays = %d", parsed.Settings.FullSyncDays)
	}
	if parsed.Settings.AttachmentPolicy != "metadata_only" {
		t.Fatalf("AttachmentPolicy = %q", parsed.Settings.AttachmentPolicy)
	}
	if parsed.Settings.SyncRevokeAsDelete {
		t.Fatal("SyncRevokeAsDelete should default false")
	}
	if !parsed.Settings.RecordParticipantsForACL {
		t.Fatal("RecordParticipantsForACL should default true")
	}
}

func TestParseConfigReadsSettings(t *testing.T) {
	parsed, err := parseConfig(validConfig())
	if err != nil {
		t.Fatalf("parseConfig error: %v", err)
	}
	if parsed.Settings.FullSyncDays != 30 {
		t.Fatalf("FullSyncDays = %d, want 30", parsed.Settings.FullSyncDays)
	}
}

func TestParseConfigRequiresCredentialsWithoutLeakingSecrets(t *testing.T) {
	cfg := validConfig()
	cfg.Credentials["private_key"] = ""
	err := parseConfigExpectError(cfg)
	if !strings.Contains(err, "private_key is required") {
		t.Fatalf("error = %q", err)
	}
	if strings.Contains(err, "top-secret") || strings.Contains(err, "BEGIN PRIVATE KEY") {
		t.Fatalf("error leaked secret material: %q", err)
	}
}

func parseConfigExpectError(cfg *types.DataSourceConfig) string {
	_, err := parseConfig(cfg)
	if err == nil {
		return ""
	}
	return err.Error()
}
