package interfaces

import (
	"context"
	"errors"
	"io"
)

// LegacyDocNormalizer is an optional capability of the internal gRPC reader.
// HTTP readers and older DocReader deployments fall back to unsupported preview.
// It reads a bounded source and returns validated DOCX bytes without persistence.
type LegacyDocNormalizer interface {
	NormalizeLegacyDoc(context.Context, io.Reader) ([]byte, error)
}

// ErrLegacyDocPreviewBusy indicates temporary converter saturation.
var ErrLegacyDocPreviewBusy = errors.New("legacy Word preview busy")
