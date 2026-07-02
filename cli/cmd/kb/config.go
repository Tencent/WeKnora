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
// `kb config`. Mirrors client.KBModelConfigView (secret-free — no api keys).
var kbConfigFields = []string{
	"retrieval_ready", "embedding", "llm", "rerank", "multimodal",
}

// ConfigService is the narrow SDK surface this command depends on.
type ConfigService interface {
	GetInitializationConfig(ctx context.Context, kbID string) (*sdk.KBModelConfigView, error)
}

// NewCmdConfig builds `weknora kb config <kb-id>` — read-only inspection of a
// knowledge base's model configuration. Write it with `weknora kb config set`.
func NewCmdConfig(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config <kb-id>",
		Short: "Show a knowledge base's model configuration (set it with `config set`)",
		Long: `Show the model configuration bound to a knowledge base: embedding, llm
(chat), rerank, and multimodal model names + source. retrieval_ready is false
until an embedding model is bound — configure it with 'weknora kb config set'.
Provider API keys are never shown.`,
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
	// `kb config` reads (this command's RunE); `kb config set` writes. Same
	// read/write pairing as mainstream config surfaces.
	cmd.AddCommand(newKBModelWriteCmd(f, "set <kb-id>", []string{"weknora", "kb", "config", "set"}))
	cmdutil.AddFormatFlag(cmd, kbConfigFields...)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "show a KB's model config (embedding/llm/rerank/multimodal by name, secret-free). retrieval_ready=false => run `weknora kb config set`. Write config with `weknora kb config set`.",
		RequiredFlags: []string{"<kb-id> (positional)"},
		Examples:      []string{"weknora kb config kb_abc --jq .data.embedding_model_id"},
		Output:        "envelope.data is {retrieval_ready, embedding{configured,model_name,source,dimension}, llm{...}, rerank{enabled,model_name}, multimodal{enabled}} — secret-free (no provider api keys)",
	})
	return cmd
}

func runConfig(ctx context.Context, fopts *cmdutil.FormatOptions, svc ConfigService, kbID string) error {
	cfg, err := svc.GetInitializationConfig(ctx, kbID)
	if err != nil {
		return cmdutil.WrapHTTP(err, "get config for knowledge base %q", kbID)
	}
	if cfg == nil {
		cfg = &sdk.KBModelConfigView{}
	}
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, cfg, nil)
	}
	w := iostreams.IO.Out
	fmt.Fprintf(w, "%-13s %v\n", "RETRIEVAL:", readyLabel(cfg.RetrievalReady))
	fmt.Fprintf(w, "%-13s %s\n", "EMBEDDING:", slotLabel(cfg.Embedding))
	fmt.Fprintf(w, "%-13s %s\n", "CHAT (LLM):", slotLabel(cfg.LLM))
	fmt.Fprintf(w, "%-13s %s\n", "RERANK:", rerankLabel(cfg.Rerank))
	fmt.Fprintf(w, "%-13s %v\n", "MULTIMODAL:", cfg.Multimodal.Enabled)
	if !cfg.RetrievalReady {
		fmt.Fprintln(w, "\n(not retrieval-ready — no embedding model; run `weknora kb config set <kb-id> --chat-model <id> --embedding-model <id>`)")
	}
	return nil
}

func readyLabel(ready bool) string {
	if ready {
		return "ready"
	}
	return "NOT ready (no embedding model)"
}

func slotLabel(s sdk.ModelSlotView) string {
	if !s.Configured {
		return "(unset)"
	}
	if s.Source != "" {
		return fmt.Sprintf("%s (%s)", s.ModelName, s.Source)
	}
	return s.ModelName
}

func rerankLabel(r sdk.RerankSlotView) string {
	if !r.Enabled {
		return "(disabled)"
	}
	return r.ModelName
}

// compile-time check: the production SDK client implements ConfigService.
var _ ConfigService = (*sdk.Client)(nil)
