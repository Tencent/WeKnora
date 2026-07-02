package kb

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

type ConfigSetOptions struct {
	ChatModel      string
	EmbeddingModel string
	Yes            bool
	DryRun         bool
}

// ConfigSetService is the narrow SDK surface this command depends on. SetKBModelConfig
// points the KB at already-registered models; GetInitializationConfig re-reads
// the server's resulting state so the success envelope reflects what stuck.
type ConfigSetService interface {
	SetKBModelConfig(ctx context.Context, kbID string, cfg *sdk.KBModelConfig) error
	GetInitializationConfig(ctx context.Context, kbID string) (*sdk.KBModelConfigView, error)
}

// newKBModelWriteCmd builds the `kb config set` model-binding write command.
// head is the argv prefix used for the risk action and retry_argv (weknora kb
// config set).
func newKBModelWriteCmd(f *cmdutil.Factory, use string, head []string) *cobra.Command {
	opts := &ConfigSetOptions{}
	action := strings.Join(head[1:], ".") // e.g. "kb.config.set"
	cmd := &cobra.Command{
		Use:   use,
		Short: "Bind embedding + chat models to a knowledge base (make it usable)",
		Long: `Bind already-registered models to a knowledge base so it can embed, retrieve,
and generate. Both --chat-model (LLM, used for generation/summary) and
--embedding-model (used for retrieval) are required; register models first with
'weknora model create' and discover ids with 'weknora model list'.

High-risk write: changing a KB's embedding model affects how its content is
indexed and searched (the server refuses to CHANGE it once the KB has
documents; setting it on an unconfigured KB is allowed). Without -y/--yes in a
non-TTY / JSON context it exits 10 (input.confirmation_required) without
applying the change.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			opts.Yes, _ = c.Flags().GetBool("yes")
			kbID := args[0]
			// Validate required flags before the dry-run gate so --dry-run rejects
			// identically to the live path.
			if err := validateConfigSetFlags(opts); err != nil {
				return err
			}
			if handled, err := cmdutil.HandleDryRun(c, opts.DryRun, cmdutil.DryRunPlan{
				Action: action,
				Args:   map[string]any{"kb": kbID, "chat_model": opts.ChatModel, "embedding_model": opts.EmbeddingModel},
			}); handled {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDestructive(f.Prompter(), opts.Yes, fopts.WantsJSON(),
				"configure", "knowledge base", kbID, action,
				cmdutil.BuildRetryArgv(c, append(append([]string{}, head...), kbID), "chat-model", "embedding-model", "format")); err != nil {
				return err
			}
			// Resolve name-or-id for the model flags (a UUID passes through; a
			// name is looked up among models of the expected type). Network read
			// on the live path only — the dry-run above shows the raw refs.
			if opts.ChatModel, err = cmdutil.ResolveModelRef(c.Context(), cli, opts.ChatModel, "KnowledgeQA"); err != nil {
				return err
			}
			if opts.EmbeddingModel, err = cmdutil.ResolveModelRef(c.Context(), cli, opts.EmbeddingModel, "Embedding"); err != nil {
				return err
			}
			return runConfigSet(c.Context(), opts, fopts, cli, kbID)
		},
	}
	cmd.Flags().StringVar(&opts.ChatModel, "chat-model", "", "Chat / LLM model id or name for generation & summary (required) — see `weknora model list`")
	cmd.Flags().StringVar(&opts.EmbeddingModel, "embedding-model", "", "Embedding model id or name for retrieval (required) — see `weknora model list`")
	cmdutil.AddFormatFlag(cmd, kbConfigFields...)
	cmdutil.AddDryRunFlag(cmd, &opts.DryRun)
	cmdutil.SetRisk(cmd, action)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "bind models to a KB so it becomes retrieval-ready. --chat-model and --embedding-model are required and accept a model id or name; discover them with `weknora model list`. Read the result back with `weknora kb config`.",
		RequiredFlags: []string{"<kb-id> (positional)", "--chat-model", "--embedding-model"},
		Examples: []string{
			"weknora kb config set kb_abc --chat-model model_llm --embedding-model model_emb -y",
		},
		Output: "envelope.data is the resulting secret-free config view {retrieval_ready, embedding, llm, rerank, multimodal}",
		Warnings: []string{
			"Requires explicit user approval (exit 10 / input.confirmation_required); never auto-add -y.",
			"The server refuses to CHANGE the embedding model of a KB that already has documents (setting it on an unconfigured KB is fine).",
		},
	})
	return cmd
}

func validateConfigSetFlags(opts *ConfigSetOptions) error {
	var missing []string
	if strings.TrimSpace(opts.ChatModel) == "" {
		missing = append(missing, "--chat-model")
	}
	if strings.TrimSpace(opts.EmbeddingModel) == "" {
		missing = append(missing, "--embedding-model")
	}
	if len(missing) == 0 {
		return nil
	}
	return &cmdutil.Error{
		Code:    cmdutil.CodeInputMissingFlag,
		Message: "kb config set requires " + strings.Join(missing, " and "),
		Hint:    "discover model ids with `weknora model list` (or register one with `weknora model create`), then pass --chat-model <id> --embedding-model <id>",
	}
}

func runConfigSet(ctx context.Context, opts *ConfigSetOptions, fopts *cmdutil.FormatOptions, svc ConfigSetService, kbID string) error {
	if err := validateConfigSetFlags(opts); err != nil {
		return err
	}
	cfg := &sdk.KBModelConfig{
		LLMModelID:       opts.ChatModel,
		EmbeddingModelID: opts.EmbeddingModel,
	}
	if err := svc.SetKBModelConfig(ctx, kbID, cfg); err != nil {
		return cmdutil.WrapHTTP(err, "configure knowledge base %q", kbID)
	}
	// Re-read the server's resulting state (secret-free view) so the envelope
	// reflects what stuck — the same shape `kb config` returns.
	result, err := svc.GetInitializationConfig(ctx, kbID)
	if err != nil || result == nil {
		// The write succeeded; surface what we applied if the read-back failed.
		result = &sdk.KBModelConfigView{
			RetrievalReady: opts.EmbeddingModel != "",
			Embedding:      sdk.ModelSlotView{Configured: opts.EmbeddingModel != "", ModelName: opts.EmbeddingModel},
			LLM:            sdk.ModelSlotView{Configured: opts.ChatModel != "", ModelName: opts.ChatModel},
		}
	}
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, result, nil)
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Configured knowledge base %s (chat: %s, embedding: %s)\n",
		kbID, orUnset(result.LLM.ModelName), orUnset(result.Embedding.ModelName))
	return nil
}

func orUnset(s string) string {
	if s == "" {
		return "(unset)"
	}
	return s
}

// compile-time check: the production SDK client implements ConfigSetService.
var _ ConfigSetService = (*sdk.Client)(nil)
