package agentcmd

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/text"
	sdk "github.com/Tencent/WeKnora/client"
)

// promptPreviewWidth caps inline KV row prompt previews. Multi-line prompts
// collapse to one line via text.OneLine; the Templates section gets the
// full multi-line treatment instead.
const promptPreviewWidth = 80

// agentViewFields enumerates fields surfaced for `--json=` field discovery
// on `agent view`. Only top-level Agent keys are listed because the
// `--json=foo,bar` field-projection filter matches flat top-level keys
// (see internal/format/filter.go). Nested AgentConfig fields are reachable
// via `--jq '.config.system_prompt'` or by selecting `config` whole and
// post-processing — listing them here would misleadingly advertise a
// projection path that does not actually filter to them.
var agentViewFields = []string{
	"id", "name", "description", "avatar",
	"is_builtin", "tenant_id", "created_by", "config",
	"created_at", "updated_at",
}

// ViewService is the narrow SDK surface this command depends on.
type ViewService interface {
	GetAgent(ctx context.Context, agentID string) (*sdk.Agent, error)
}

// NewCmdView builds `weknora agent view <agent-id>`.
func NewCmdView(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "view <agent-id>",
		Short: "Show a custom agent's configuration",
		Long: `Renders the agent's metadata and full AgentConfig as grouped KV
sections (Identity / LLM / KB attachment / Retrieval / Query rewrite /
Tools / FAQ / Web search / Multi-turn / Fallback / Templates). Zero-value
fields are omitted; sections with no set fields are suppressed entirely.

Pass --json for the bare SDK Agent object (config nested, not flattened).
Field projection (--json=id,name) works on top-level keys only; reach
nested config fields via --jq.`,
		Example: `  weknora agent view ag_abc
  weknora agent view ag_abc --json=id,name,config       # top-level projection
  weknora agent view ag_abc --json --jq '.config.system_prompt'`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			jopts, err := cmdutil.CheckJSONFlags(c)
			if err != nil {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runView(c.Context(), jopts, cli, args[0])
		},
	}
	cmdutil.AddJSONFlags(cmd, agentViewFields)
	return cmd
}

func runView(ctx context.Context, jopts *cmdutil.JSONOptions, svc ViewService, agentID string) error {
	a, err := svc.GetAgent(ctx, agentID)
	if err != nil {
		return cmdutil.WrapHTTP(err, "fetch agent %s", agentID)
	}
	if jopts.Enabled() {
		return jopts.Emit(iostreams.IO.Out, a)
	}
	renderAgent(iostreams.IO.Out, a)
	return nil
}

// renderAgent prints a single agent in human-readable form, grouped into
// 10 presentation sections. Zero-value fields are omitted; a section
// header prints only when at least one of its fields is set. Group
// labels and order are pinned by snapshot tests so future drift surfaces
// as test failure rather than silent divergence.
func renderAgent(w io.Writer, a *sdk.Agent) {
	// Identity is always rendered — id/name/created_at/updated_at are
	// never meaningfully empty for a fetched Agent.
	fmt.Fprintln(w, "Identity:")
	fmt.Fprintf(w, "  ID:           %s\n", a.ID)
	fmt.Fprintf(w, "  Name:         %s\n", a.Name)
	if a.Description != "" {
		fmt.Fprintf(w, "  Description:  %s\n", a.Description)
	}
	if a.IsBuiltin {
		fmt.Fprintln(w, "  Builtin:      yes")
	}
	if a.CreatedBy != "" {
		fmt.Fprintf(w, "  Created by:   %s\n", a.CreatedBy)
	}
	if a.TenantID != 0 {
		fmt.Fprintf(w, "  Tenant ID:    %d\n", a.TenantID)
	}
	fmt.Fprintf(w, "  Created at:   %s\n", a.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(w, "  Updated at:   %s\n", a.UpdatedAt.Format("2006-01-02 15:04:05"))

	if a.Config == nil {
		return
	}
	c := a.Config

	// Each group is rendered via a tiny helper: collect its set rows
	// upfront, suppress the whole section if empty. Avoids the
	// "header printed but no body" failure mode that plagues naive
	// conditional rendering.
	type row struct{ k, v string }
	emit := func(label string, rows []row) {
		if len(rows) == 0 {
			return
		}
		fmt.Fprintln(w)
		fmt.Fprintf(w, "%s:\n", label)
		for _, r := range rows {
			fmt.Fprintf(w, "  %-32s %s\n", r.k+":", r.v)
		}
	}

	// LLM
	llm := []row{}
	if c.ModelID != "" {
		llm = append(llm, row{"Model ID", c.ModelID})
	}
	if c.RerankModelID != "" {
		llm = append(llm, row{"Rerank model ID", c.RerankModelID})
	}
	if c.Temperature != 0 {
		llm = append(llm, row{"Temperature", fmt.Sprintf("%g", c.Temperature)})
	}
	if c.MaxCompletionTokens != 0 {
		llm = append(llm, row{"Max completion tokens", fmt.Sprintf("%d", c.MaxCompletionTokens)})
	}
	if c.MaxIterations != 0 {
		llm = append(llm, row{"Max iterations", fmt.Sprintf("%d", c.MaxIterations)})
	}
	if c.AgentMode != "" {
		llm = append(llm, row{"Mode", c.AgentMode})
	}
	emit("LLM", llm)

	// KB attachment
	kb := []row{}
	if c.KBSelectionMode != "" {
		kb = append(kb, row{"KB selection mode", c.KBSelectionMode})
	}
	if len(c.KnowledgeBases) > 0 {
		kb = append(kb, row{"Knowledge bases", strings.Join(c.KnowledgeBases, ", ")})
	}
	emit("KB attachment", kb)

	// Retrieval
	retr := []row{}
	if c.EmbeddingTopK != 0 {
		retr = append(retr, row{"Embedding top K", fmt.Sprintf("%d", c.EmbeddingTopK)})
	}
	if c.KeywordThreshold != 0 {
		retr = append(retr, row{"Keyword threshold", fmt.Sprintf("%g", c.KeywordThreshold)})
	}
	if c.VectorThreshold != 0 {
		retr = append(retr, row{"Vector threshold", fmt.Sprintf("%g", c.VectorThreshold)})
	}
	if c.RerankTopK != 0 {
		retr = append(retr, row{"Rerank top K", fmt.Sprintf("%d", c.RerankTopK)})
	}
	if c.RerankThreshold != 0 {
		retr = append(retr, row{"Rerank threshold", fmt.Sprintf("%g", c.RerankThreshold)})
	}
	emit("Retrieval", retr)

	// Query rewrite
	qr := []row{}
	if c.EnableQueryExpansion {
		qr = append(qr, row{"Query expansion", "enabled"})
	}
	if c.EnableRewrite {
		qr = append(qr, row{"Rewrite", "enabled"})
	}
	if c.QueryUnderstandModelID != "" {
		qr = append(qr, row{"Query understand model ID", c.QueryUnderstandModelID})
	}
	if c.RewritePromptSystem != "" {
		qr = append(qr, row{"Rewrite prompt (system)", text.OneLine(promptPreviewWidth, c.RewritePromptSystem)})
	}
	if c.RewritePromptUser != "" {
		qr = append(qr, row{"Rewrite prompt (user)", text.OneLine(promptPreviewWidth, c.RewritePromptUser)})
	}
	emit("Query rewrite", qr)

	// Tools
	tools := []row{}
	if len(c.AllowedTools) > 0 {
		tools = append(tools, row{"Allowed tools", strings.Join(c.AllowedTools, ", ")})
	}
	if c.MCPSelectionMode != "" {
		tools = append(tools, row{"MCP selection mode", c.MCPSelectionMode})
	}
	if len(c.MCPServices) > 0 {
		tools = append(tools, row{"MCP services", strings.Join(c.MCPServices, ", ")})
	}
	if len(c.SupportedFileTypes) > 0 {
		tools = append(tools, row{"Supported file types", strings.Join(c.SupportedFileTypes, ", ")})
	}
	emit("Tools", tools)

	// FAQ
	faq := []row{}
	if c.FAQPriorityEnabled {
		faq = append(faq, row{"FAQ priority", "enabled"})
	}
	if c.FAQDirectAnswerThreshold != 0 {
		faq = append(faq, row{"FAQ direct-answer threshold", fmt.Sprintf("%g", c.FAQDirectAnswerThreshold)})
	}
	if c.FAQScoreBoost != 0 {
		faq = append(faq, row{"FAQ score boost", fmt.Sprintf("%g", c.FAQScoreBoost)})
	}
	emit("FAQ", faq)

	// Web search
	web := []row{}
	if c.WebSearchEnabled {
		web = append(web, row{"Web search", "enabled"})
	}
	if c.WebSearchMaxResults != 0 {
		web = append(web, row{"Web search max results", fmt.Sprintf("%d", c.WebSearchMaxResults)})
	}
	emit("Web search", web)

	// Multi-turn
	mt := []row{}
	if c.MultiTurnEnabled {
		mt = append(mt, row{"Multi-turn", "enabled"})
	}
	if c.HistoryTurns != 0 {
		mt = append(mt, row{"History turns", fmt.Sprintf("%d", c.HistoryTurns)})
	}
	emit("Multi-turn", mt)

	// Fallback
	fb := []row{}
	if c.FallbackStrategy != "" {
		fb = append(fb, row{"Strategy", c.FallbackStrategy})
	}
	if c.FallbackResponse != "" {
		fb = append(fb, row{"Response", c.FallbackResponse})
	}
	if c.FallbackPrompt != "" {
		fb = append(fb, row{"Prompt", text.OneLine(promptPreviewWidth, c.FallbackPrompt)})
	}
	emit("Fallback", fb)

	// Templates — system_prompt and context_template can be multi-line;
	// render headed blocks rather than KV rows for readability.
	if c.SystemPrompt != "" || c.ContextTemplate != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Templates:")
		if c.SystemPrompt != "" {
			fmt.Fprintln(w, "  System prompt:")
			writeIndented(w, c.SystemPrompt, "    ")
		}
		if c.ContextTemplate != "" {
			fmt.Fprintln(w, "  Context template:")
			writeIndented(w, c.ContextTemplate, "    ")
		}
	}
}

// writeIndented prints s with the given prefix on every line. Trailing
// newline always added so the next section starts on its own line.
func writeIndented(w io.Writer, s, prefix string) {
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		fmt.Fprintf(w, "%s%s\n", prefix, line)
	}
}
