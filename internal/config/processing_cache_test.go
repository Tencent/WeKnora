package config

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestProcessingCacheConfiguration(t *testing.T) {
	var cfg Config
	opts := cfg.ProcessingCache.Options()
	if opts.ReadEnabled || opts.WriteEnabled || opts.MaxBytes != 1<<20 || opts.TTL != 30*24*time.Hour {
		t.Fatalf("unsafe defaults: %+v", opts)
	}
	if err := yaml.Unmarshal([]byte("processing_cache:\n  read_enabled: true\n  write_enabled: true\n  max_bytes: 4096\n  retention_days: 7\n  epoch: revision-2\n"), &cfg); err != nil {
		t.Fatal(err)
	}
	opts = cfg.ProcessingCache.Options()
	if !opts.ReadEnabled || !opts.WriteEnabled || opts.MaxBytes != 4096 || opts.TTL != 7*24*time.Hour || opts.Epoch != "revision-2" {
		t.Fatalf("configuration not applied: %+v", opts)
	}
}

func TestProcessingCacheStagePayloadBudgets(t *testing.T) {
	c := &ProcessingCacheConfig{ParsingMaxBytes: 8 << 20, ImageMaxBytes: 4 << 20}
	o := c.Options()
	if o.StageMaxBytes["document.parse.v1"] != 8<<20 || o.StageMaxBytes["image.preprocess.v1"] != 4<<20 || o.ReadEnabled || o.WriteEnabled {
		t.Fatalf("stage budgets not bounded/default off: %+v", o)
	}
}
