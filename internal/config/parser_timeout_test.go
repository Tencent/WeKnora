package config

import (
	"os"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestSelfHostedParserTimeoutConfig(t *testing.T) {
	for _, key := range []string{"WEKNORA_PADDLEOCR_VL_TIMEOUT", "WEKNORA_MINERU_TIMEOUT"} {
		t.Run(key, func(t *testing.T) {
			for _, tc := range []struct {
				name, value     string
				yamlValue, want time.Duration
			}{
				{"unset", "", 0, 1000 * time.Second},
				{"empty", "", 0, 1000 * time.Second},
				{"whitespace", "  ", 0, 1000 * time.Second},
				{"seconds", "5400s", 0, 90 * time.Minute},
				{"minutes", "90m", 0, 90 * time.Minute},
				{"trimmed", " 90m ", 0, 90 * time.Minute},
				{"invalid", "invalid", 0, 1000 * time.Second},
				{"unitless", "5400", 0, 1000 * time.Second},
				{"zero", "0s", 0, 1000 * time.Second},
				{"negative", "-1s", 0, 1000 * time.Second},
				{"overflow", "999999999999999999999h", 0, 1000 * time.Second},
				{"yaml", "", 40 * time.Minute, 40 * time.Minute},
				{"env overrides yaml", "90m", 40 * time.Minute, 90 * time.Minute},
				{"invalid env keeps yaml", "invalid", 40 * time.Minute, 40 * time.Minute},
				{"nonpositive yaml", "", -time.Second, 1000 * time.Second},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Setenv("WEKNORA_PADDLEOCR_VL_TIMEOUT", "")
					t.Setenv("WEKNORA_MINERU_TIMEOUT", "")
					t.Setenv(key, tc.value)
					if tc.name == "unset" {
						if err := os.Unsetenv(key); err != nil {
							t.Fatal(err)
						}
					}
					cfg := &Config{KnowledgeBase: &KnowledgeBaseConfig{}}
					if key == "WEKNORA_MINERU_TIMEOUT" {
						cfg.KnowledgeBase.MinerUTimeout = tc.yamlValue
					} else {
						cfg.KnowledgeBase.PaddleOCRVLTimeout = tc.yamlValue
					}
					applyKnowledgeBaseEnvOverrides(cfg)
					got, other := cfg.KnowledgeBase.PaddleOCRVLTimeout, cfg.KnowledgeBase.MinerUTimeout
					if key == "WEKNORA_MINERU_TIMEOUT" {
						got, other = other, got
					}
					if got != tc.want {
						t.Fatalf("timeout = %s, want %s", got, tc.want)
					}
					if other != 1000*time.Second {
						t.Fatalf("other parser timeout changed: %s", other)
					}
				})
			}
		})
	}
}

func TestSelfHostedParserTimeoutYAML(t *testing.T) {
	t.Setenv("WEKNORA_PADDLEOCR_VL_TIMEOUT", "")
	t.Setenv("WEKNORA_MINERU_TIMEOUT", "")
	var cfg Config
	raw := "knowledge_base:\n  paddleocr_vl_timeout: 90m\n  mineru_timeout: 40m\n"
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	applyKnowledgeBaseEnvOverrides(&cfg)
	if cfg.KnowledgeBase.PaddleOCRVLTimeout != 90*time.Minute || cfg.KnowledgeBase.MinerUTimeout != 40*time.Minute {
		t.Fatalf("YAML timeouts not preserved: %+v", cfg.KnowledgeBase)
	}
	cfg = Config{}
	applyKnowledgeBaseEnvOverrides(&cfg)
	if cfg.KnowledgeBase.PaddleOCRVLTimeout != 1000*time.Second || cfg.KnowledgeBase.MinerUTimeout != 1000*time.Second {
		t.Fatalf("missing knowledge base defaults: %+v", cfg.KnowledgeBase)
	}
}
