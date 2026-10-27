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
	// PreprocessImage straightens and crops a photographed page before OCR runs.
	//
	// The second return reports a verdict rather than a failure: fallback=true
	// means "this input is not one we can improve — keep your original bytes".
	// Callers must treat it as a normal outcome, not as an error worth logging,
	// and a transport that does not offer the capability reports it this way too.
	PreprocessImage(ctx context.Context, image []byte) (rectified []byte, fallback bool, err error)
}
