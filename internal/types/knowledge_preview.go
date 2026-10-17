package types

import "io"

// KnowledgePreviewFile is an already-authorized stream for the in-browser
// previewer.
type KnowledgePreviewFile struct {
	// Reader streams the bytes the previewer should render. The caller owns it
	// and must close it.
	Reader io.ReadCloser
	// Filename drives Content-Type / Content-Disposition; it stays the
	// original upload name even when Reader serves a converted copy.
	Filename string
	// Converted is true when Reader serves a rewritten copy (for diagnostics
	// and cache accounting).
	Converted bool
	// FallbackReason is non-empty when conversion was attempted but failed;
	// Reader then serves the original upload so the preview keeps working.
	FallbackReason string
}
