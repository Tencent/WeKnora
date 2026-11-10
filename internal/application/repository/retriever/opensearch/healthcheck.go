package opensearch

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// TestConnection verifies an OpenSearch cluster is reachable, runs a
// supported version, and has the k-NN plugin installed on every node. It is
// the connectivity probe used by the VectorStore service's CreateStore
// health-check (the driver's unexported probes are reused here). Returns a
// wrapped sentinel error on failure; nil on success.
func TestConnection(ctx context.Context, cfg *types.ConnectionConfig) error {
	client, err := NewOpenSearchClient(cfg)
	if err != nil {
		return err
	}
	if err := probeVersion(ctx, client); err != nil {
		return err
	}
	return probeKNNPlugin(ctx, client)
}
