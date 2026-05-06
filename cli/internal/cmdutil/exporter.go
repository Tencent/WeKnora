package cmdutil

import (
	"io"

	"github.com/Tencent/WeKnora/cli/internal/format"
)

// Exporter renders command output. Foundation PR ships a single
// jsonExporter that writes the envelope; PR-3 lands lipgloss tables, jq,
// and templates as additional implementations.
type Exporter interface {
	Write(w io.Writer, env format.Envelope) error
}

// NewJSONExporter returns an Exporter that emits envelope JSON via
// format.WriteEnvelope (single-source encoder config: no HTML escape).
func NewJSONExporter() Exporter { return &jsonExporter{} }

type jsonExporter struct{}

func (jsonExporter) Write(w io.Writer, env format.Envelope) error {
	return format.WriteEnvelope(w, env)
}

// NewTableExporter is a foundation-PR alias for the JSON exporter; PR-3
// replaces it with a lipgloss-based renderer that respects iostreams.IO
// ColorEnabled. Until then table output looks identical to JSON output so
// commands work end-to-end either way.
func NewTableExporter(_ []string) Exporter { return &jsonExporter{} }
