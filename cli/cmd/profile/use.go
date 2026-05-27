package profilecmd

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

// profileUseFields enumerates fields surfaced for `--format json` discovery on
// `profile use`.
var profileUseFields = []string{"current_profile", "previous_profile"}

// NewCmdUse builds the `weknora profile use <name>` command.
func NewCmdUse(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "use <name>",
		Short: "Switch the default profile for subsequent commands",
		Long: `Switches the default profile written in config.yaml. Names are case-sensitive.

The active profile is what every subsequent command uses for auth + host. The
global --profile flag (e.g. weknora --profile staging kb list) overrides for
one command without writing to disk.

AI agents: Do NOT switch the active profile unless the user explicitly asked
you to. Profile selection is a user preference; one-shot overrides should use
the global --profile flag instead, which writes nothing to disk.`,
		Example: `  weknora profile use staging               # persist switch
  weknora --profile staging kb list         # one-shot override (no disk write)
  weknora profile use staging --format json        # {current_profile, previous_profile}`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			return runUse(args[0], fopts)
		},
	}
	cmdutil.AddFormatFlag(cmd, profileUseFields...)
	return cmd
}

type useResult struct {
	CurrentProfile  string `json:"current_profile"`
	PreviousProfile string `json:"previous_profile,omitempty"`
}

func runUse(name string, fopts *cmdutil.FormatOptions) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if _, ok := cfg.Profiles[name]; !ok {
		return notFoundError(name, cfg)
	}
	prev := cfg.CurrentProfile
	cfg.CurrentProfile = name
	if err := config.Save(cfg); err != nil {
		return err
	}
	result := useResult{CurrentProfile: name, PreviousProfile: prev}
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, result, nil)
	}
	if prev != "" && prev != name {
		fmt.Fprintf(iostreams.IO.Out, "✓ Switched profile to %s (was %s)\n", name, prev)
	} else {
		fmt.Fprintf(iostreams.IO.Out, "✓ Active profile: %s\n", name)
	}
	return nil
}

func notFoundError(name string, cfg *config.Config) error {
	if len(cfg.Profiles) == 0 {
		return &cmdutil.Error{
			Code:    cmdutil.CodeLocalProfileNotFound,
			Message: fmt.Sprintf("profile not found: %s", name),
			Hint:    "no profiles registered - run `weknora auth login` first",
		}
	}
	keys := profileKeys(cfg.Profiles)
	candidate := closestMatch(name, keys)
	var hint string
	if candidate != "" && candidate != name {
		hint = fmt.Sprintf("did you mean: %q?", candidate)
	} else {
		hint = fmt.Sprintf("available profiles: %v", keys)
	}
	return &cmdutil.Error{
		Code:    cmdutil.CodeLocalProfileNotFound,
		Message: fmt.Sprintf("profile not found: %s", name),
		Hint:    hint,
	}
}

func profileKeys(m map[string]config.Profile) []string {
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
