package contextcmd

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

// contextUseFields enumerates fields surfaced for `--json` discovery on
// `context use`.
var contextUseFields = []string{"current_context", "previous_context"}

// NewCmdUse builds the `weknora context use <name>` command.
func NewCmdUse(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "use <name>",
		Short: "Switch the default context for subsequent commands",
		Long: `Switches the default context written in config.yaml. Names are case-sensitive.

The active context is what every subsequent command uses for auth + host. The
global --context flag (e.g. weknora --context staging kb list) overrides for
one command without writing to disk.

AI agents: Do NOT switch the active context unless the user explicitly asked
you to. Context selection is a user preference; one-shot overrides should use
the global --context flag instead, which writes nothing to disk.`,
		Example: `  weknora context use staging               # persist switch
  weknora --context staging kb list         # one-shot override (no disk write)
  weknora context use staging --json        # {current_context, previous_context}`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			jopts, err := cmdutil.CheckJSONFlags(c)
			if err != nil {
				return err
			}
			return runUse(args[0], jopts)
		},
	}
	cmdutil.AddJSONFlags(cmd, contextUseFields)
	return cmd
}

type useResult struct {
	CurrentContext  string `json:"current_context"`
	PreviousContext string `json:"previous_context,omitempty"`
}

func runUse(name string, jopts *cmdutil.JSONOptions) error {
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
	result := useResult{CurrentContext: name, PreviousContext: prev}
	if jopts.Enabled() {
		return jopts.Emit(iostreams.IO.Out, result)
	}
	if prev != "" && prev != name {
		fmt.Fprintf(iostreams.IO.Out, "✓ Switched context to %s (was %s)\n", name, prev)
	} else {
		fmt.Fprintf(iostreams.IO.Out, "✓ Active context: %s\n", name)
	}
	return nil
}

func notFoundError(name string, cfg *config.Config) error {
	if len(cfg.Contexts) == 0 {
		return &cmdutil.Error{
			Code:    cmdutil.CodeLocalContextNotFound,
			Message: fmt.Sprintf("context not found: %s", name),
			Hint:    "no contexts registered - run `weknora auth login` first",
		}
	}
	keys := contextKeys(cfg.Contexts)
	candidate := closestMatch(name, keys)
	var hint string
	if candidate != "" && candidate != name {
		hint = fmt.Sprintf("did you mean: %q?", candidate)
	} else {
		hint = fmt.Sprintf("available contexts: %v", keys)
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

// closestMatch returns the candidate with min levenshtein distance ≤ 2,
// or "" if none qualifies. Ties broken by lexicographic order so the hint
// is deterministic across map-iteration orderings (Go randomizes range over
// map; without this, did-you-mean output is flaky for equally-close
// candidates).
func closestMatch(target string, candidates []string) string {
	sorted := append([]string(nil), candidates...)
	sort.Strings(sorted)
	best := ""
	bestD := 3
	for _, c := range sorted {
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
