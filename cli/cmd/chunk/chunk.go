// Package chunkcmd implements the `chunk` command subtree for managing
// document chunks in a knowledge base. The directory is named `chunk/`
// to match the cobra subcommand; the Go package is `chunkcmd` to avoid
// colliding with cobra's *cobra.Command identifier.
//
// "chunk" in this subtree refers to indexed pieces of a knowledge
// document (server resource: GET/DELETE /chunks/...). Each document
// has many chunks; the chunking pipeline produces them at ingest time.
package chunkcmd

import (
	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
)

const chunkLong = `Manage and inspect document chunks.

Chunks are the indexed pieces of a knowledge document (1 doc → many chunks).
Use 'chunk list' to enumerate chunks in stored order (RAG admin / debug).`

// NewCmdChunk builds the parent `chunk` command. Called from cli/cmd/root.go.
func NewCmdChunk(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chunk <subcommand>",
		Short: "Manage document chunks (RAG retrieval debug)",
		Long:  chunkLong,
	}
	cmd.AddCommand(NewCmdList(f))
	cmd.AddCommand(NewCmdView(f))
	cmd.AddCommand(NewCmdDelete(f))
	return cmd
}
