package cmdutil

import (
	"io"

	"github.com/Tencent/WeKnora/cli/internal/format"
)

// Exporter renders an envelope to a writer. Currently the only
// implementation is the JSON exporter; the interface stays in case a future
// renderer (templated text, table) needs to plug in without changing call
// sites that already write through Exporter.Write.
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
