package file

import "errors"

// ErrCrossBackendCopy is returned by CopyFile implementations when the source
// path belongs to a different storage provider than the destination service.
// PR1 only supports same-backend (server-side) copies; cross-backend streaming
// copy is intentionally not implemented yet.
var ErrCrossBackendCopy = errors.New("file: cross-backend copy not supported")
