package kb

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// kbConfigFields enumerates the fields surfaced for `--format json` discovery on
// `kb config`. Mirrors client.InitializationConfig.
var kbConfigFields = []string{
	"chat_model_id", "embedding_model_id", "rerank_model_id", "multimodal_id",
}

// ConfigService is the narrow SDK surface this command depends on.
type ConfigService interface {
	GetInitializationConfig(ctx context.Context, kbID string) (*sdk.InitializationConfig, error)
}

// NewCmdConfig builds `weknora kb config <kb-id>` — read-only inspection of a
// knowledge base's model configuration (set it with `weknora kb init`).
func NewCmdConfig(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config <kb-id>",
		Short: "Show a knowledge base's model configuration",
		Long: `Show the model configuration bound to a knowledge base: chat, embedding,
rerank, and multimodal model ids. An empty embedding_model_id means the KB is
not yet usable for retrieval — configure it with 'weknora kb init'.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runConfig(c.Context(), fopts, cli, args[0])
		},
	}
	cmdutil.AddFormatFlag(cmd, kbConfigFields...)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "show a KB's model config (chat/embedding/rerank/multimodal model ids). Empty embedding_model_id => not retrieval-ready; run `weknora kb init`.",
		RequiredFlags: []string{"<kb-id> (positional)"},
		Examples:      []string{"weknora kb config kb_abc --jq .data.embedding_model_id"},
		Output:        "envelope.data is {chat_model_id, embedding_model_id, rerank_model_id, multimodal_id}",
	})
	return cmd
}

func runConfig(ctx context.Context, fopts *cmdutil.FormatOptions, svc ConfigService, kbID string) error {
	cfg, err := svc.GetInitializationConfig(ctx, kbID)
	if err != nil {
		return cmdutil.WrapHTTP(err, "get config for knowledge base %q", kbID)
	}
	if cfg == nil {
		cfg = &sdk.InitializationConfig{}
	}
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, cfg, nil)
	}
	w := iostreams.IO.Out
	fmt.Fprintf(w, "%-11s %s\n", "EMBEDDING:", orNone(cfg.EmbeddingModelID))
	fmt.Fprintf(w, "%-11s %s\n", "CHAT:", orNone(cfg.ChatModelID))
	fmt.Fprintf(w, "%-11s %s\n", "RERANK:", orNone(cfg.RerankModelID))
	fmt.Fprintf(w, "%-11s %s\n", "MULTIMODAL:", orNone(cfg.MultimodalID))
	if cfg.EmbeddingModelID == "" {
		fmt.Fprintln(w, "\n(no embedding model set — run `weknora kb init <kb-id> --embedding-model <id>`)")
	}
	return nil
}

func orNone(s string) string {
	if s == "" {
		return "(unset)"
	}
	return s
}

// compile-time check: the production SDK client implements ConfigService.
var _ ConfigService = (*sdk.Client)(nil)
