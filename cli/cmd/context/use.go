package contextcmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/agent"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

// NewCmdUse builds the `weknora context use <name>` command.
func NewCmdUse(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "use <name>",
		Short: "Switch the default context for subsequent commands",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runUse(args[0])
		},
	}
	agent.SetAgentHelp(cmd, "Switches default CLI context. Returns previous_context + current_context. Errors with hint when name unknown.")
	return cmd
}

type useResult struct {
	CurrentContext  string `json:"current_context"`
	PreviousContext string `json:"previous_context,omitempty"`
}

func runUse(name string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if _, ok := cfg.Contexts[name]; !ok {
		return notFoundError(name, cfg)
	}
	prev := cfg.CurrentContext
	cfg.CurrentContext = name
	if err := config.Save(cfg); err != nil {
		return err
	}
	return format.WriteEnvelope(iostreams.IO.Out, format.Success(useResult{
		CurrentContext:  name,
		PreviousContext: prev,
	}, nil))
}

func notFoundError(name string, cfg *config.Config) error {
	if len(cfg.Contexts) == 0 {
		return &cmdutil.Error{
			Code:    cmdutil.CodeLocalContextNotFound,
			Message: fmt.Sprintf("context not found: %s", name),
			Hint:    "no contexts registered — run `weknora auth login` first",
		}
	}
	candidate := closestMatch(name, contextKeys(cfg.Contexts))
	hint := availableHint(cfg)
	if candidate != "" && candidate != name {
		hint = fmt.Sprintf("did you mean: %q?", candidate)
	}
	return &cmdutil.Error{
		Code:    cmdutil.CodeLocalContextNotFound,
		Message: fmt.Sprintf("context not found: %s", name),
		Hint:    hint,
	}
}

func contextKeys(m map[string]config.Context) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func availableHint(cfg *config.Config) string {
	return fmt.Sprintf("available contexts: %v", contextKeys(cfg.Contexts))
}

// closestMatch returns the candidate with min levenshtein distance ≤ 2,
// or "" if none qualifies.
func closestMatch(target string, candidates []string) string {
	best := ""
	bestD := 3
	for _, c := range candidates {
		d := levenshtein(target, c)
		if d < bestD {
			bestD = d
			best = c
		}
	}
	if bestD > 2 {
		return ""
	}
	return best
}

func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}
