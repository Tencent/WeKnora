package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// DocReader is the core interface for reading documents into markdown.
type DocReader interface {
	Read(ctx context.Context, req *types.ReadRequest) (*types.ReadResult, error)
}

// PreviewConverter is the optional capability of a document reader to turn a
// document into a browser-renderable preview copy. It is deliberately separate
// from DocumentReader: the HTTP transport and test doubles do not have to
// implement it, and callers fall back to the original file when the concrete
// reader does not satisfy it.
type PreviewConverter interface {
	ConvertPreview(ctx context.Context, req *types.ConvertPreviewRequest) (*types.ConvertPreviewResult, error)
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
