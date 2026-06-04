package container

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestBuildMilvusClientConfig_UsesDatabaseName(t *testing.T) {
	cfg := buildMilvusClientConfig(types.ConnectionConfig{
		Addr:     "milvus.example.com:19530",
		Username: "tester",
		Password: "secret",
		Database: "regdi_ram_haom1",
	})

	if cfg.Address != "milvus.example.com:19530" {
		t.Fatalf("expected address to be preserved, got %q", cfg.Address)
	}
	if cfg.Username != "tester" {
		t.Fatalf("expected username to be preserved, got %q", cfg.Username)
	}
	if cfg.Password != "secret" {
		t.Fatalf("expected password to be preserved, got %q", cfg.Password)
	}
	if cfg.DBName != "regdi_ram_haom1" {
		t.Fatalf("expected DBName to be propagated, got %q", cfg.DBName)
	}
	if len(cfg.DialOptions) != 1 {
		t.Fatalf("expected one dial option, got %d", len(cfg.DialOptions))
	}
}

func TestBuildMilvusClientConfig_DefaultsAddressWhenMissing(t *testing.T) {
	cfg := buildMilvusClientConfig(types.ConnectionConfig{})

	if cfg.Address != "localhost:19530" {
		t.Fatalf("expected default address localhost:19530, got %q", cfg.Address)
	}
	if cfg.DBName != "" {
		t.Fatalf("expected empty DBName by default, got %q", cfg.DBName)
	}
	if len(cfg.DialOptions) != 1 {
		t.Fatalf("expected one dial option, got %d", len(cfg.DialOptions))
	}
}
