package container

import (
	"context"
	"errors"
	"testing"
	"time"

	milvusclient "github.com/milvus-io/milvus/client/v2/milvusclient"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func whitelistOnly(t *testing.T, whitelist, only string) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", whitelist)
	t.Setenv("SSRF_WHITELIST_EXTRA", "")
	t.Setenv("SSRF_DNS_WHITELIST_ONLY", only)
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
}

// gRPC resolves its own target before any dialer runs, so an env-configured
// vector store host can only be judged before the client exists (#3378).
// Both helpers must refuse without opening a connection.
func TestEnvVectorStoreClientsRefuseNonWhitelistedHost(t *testing.T) {
	whitelistOnly(t, "qdrant,milvus", "true")

	_, err := newEnvQdrantClient("outside.invalid", 6334, "", false)
	if !errors.Is(err, secutils.ErrSSRFHostNotWhitelisted) {
		t.Fatalf("qdrant: expected the whitelist refusal, got %v", err)
	}

	cfg := &milvusclient.ClientConfig{Address: "outside.invalid:19530"}
	if _, err := newEnvMilvusClient(context.Background(), cfg); !errors.Is(err, secutils.ErrSSRFHostNotWhitelisted) {
		t.Fatalf("milvus: expected the whitelist refusal, got %v", err)
	}

	// The address carries a port the whitelist never matches on; the host
	// inside it is what must be compared. Past the name check the real client
	// tries to connect, so this one gets a deadline of its own.
	dialCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cfg = &milvusclient.ClientConfig{Address: "milvus:19530"}
	if _, err := newEnvMilvusClient(dialCtx, cfg); errors.Is(err, secutils.ErrSSRFHostNotWhitelisted) {
		t.Fatalf("a whitelisted host must pass the name check, got %v", err)
	}
}

func TestEnvVectorStoreClientsIgnoreTheWhitelistWhenTheModeIsOff(t *testing.T) {
	whitelistOnly(t, "qdrant", "false")

	_, err := newEnvQdrantClient("outside.invalid", 6334, "", false)
	if errors.Is(err, secutils.ErrSSRFHostNotWhitelisted) {
		t.Fatalf("with the mode off nothing is refused for being off-whitelist, got %v", err)
	}
}
