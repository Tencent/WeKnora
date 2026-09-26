package im

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/plugin/configschema"
)

// registeredPlatforms are the adapters container.registerIMService wires.
var registeredPlatforms = []string{
	"wecom", "feishu", "lark", "slack", "telegram", "dingtalk", "mattermost", "wechat", "qqbot", "yunzhijia",
}

func TestEveryPlatformHasValidMetadata(t *testing.T) {
	for _, id := range registeredPlatforms {
		info := LookupPlatformInfo(id)
		if info.Order >= 1000 {
			t.Errorf("%s: no metadata entry", id)
			continue
		}
		if len(info.Modes) == 0 {
			t.Errorf("%s: no connection mode", id)
		}
		if info.ConfigSchema == nil {
			t.Errorf("%s: no config schema", id)
			continue
		}
		if err := info.ConfigSchema.Check(); err != nil {
			t.Errorf("%s: invalid schema: %v", id, err)
		}
		// Mode-dependent fields may only name modes the platform supports.
		for key, prop := range info.ConfigSchema.Properties {
			if mode, ok := prop.VisibleIf["$mode"]; ok && !contains(info.Modes, mode.(string)) {
				t.Errorf("%s.%s: visible only in unsupported mode %q", id, key, mode)
			}
		}
	}
}

func TestWeComFieldsFollowTheMode(t *testing.T) {
	s := LookupPlatformInfo("wecom").ConfigSchema
	visibleIn := func(mode string) map[string]bool {
		out := map[string]bool{}
		for key, prop := range s.Properties {
			if m, ok := prop.VisibleIf["$mode"]; !ok || m == mode {
				out[key] = true
			}
		}
		return out
	}
	ws, hook := visibleIn(ModeWebSocket), visibleIn(ModeWebhook)
	if !ws["bot_id"] || ws["corp_id"] || !hook["corp_id"] || hook["bot_secret"] {
		t.Fatalf("websocket=%v webhook=%v", ws, hook)
	}
}

func TestYunzhijiaRequiresTheSendURL(t *testing.T) {
	s := LookupPlatformInfo("yunzhijia").ConfigSchema
	errs := configschema.ValidateInContext(s, map[string]any{
		"allowed_webhook_host_suffix": "yunzhijia.com", "timeout_seconds": 10.0,
	}, map[string]any{"mode": ModeWebhook})
	if len(errs) != 1 || errs[0].Path != "send_msg_url" || errs[0].Code != configschema.CodeRequired {
		t.Fatalf("want send_msg_url required, got %v", errs)
	}
	errs = configschema.Validate(s, map[string]any{
		"send_msg_url": "https://x", "allowed_webhook_host_suffix": "yunzhijia.com", "timeout_seconds": 99.0,
	})
	if len(errs) != 1 || errs[0].Code != configschema.CodeMaximum {
		t.Fatalf("want timeout maximum error, got %v", errs)
	}
}

func TestUnknownPlatformFallsBack(t *testing.T) {
	info := LookupPlatformInfo("brand-new")
	if info.Name != "brand-new" || len(info.Modes) != 1 {
		t.Fatalf("fallback = %+v", info)
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
