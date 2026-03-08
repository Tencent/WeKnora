package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// DocReader is the core interface for reading documents into markdown.
type DocReader interface {
	Read(ctx context.Context, req *types.ReadRequest) (*types.ReadResult, error)
}

// DocumentReader extends DocReader with transport lifecycle management
// and remote engine discovery. Used by gRPC/HTTP clients that talk to
// the Python docreader service.
type DocumentReader interface {
	DocReader
	Reconnect(addr string) error
	IsConnected() bool
	// ListEngines queries the remote docreader for its registered parser engines.
	// Returns engines the remote service supports, allowing auto-discovery of
	// newly added engines without Go code changes.
	ListEngines(ctx context.Context, overrides map[string]string) ([]types.ParserEngineInfo, error)
}
