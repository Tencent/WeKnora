// Package skillparity contains the K6 drift guard: every weknora command and
// long flag referenced in a bundled Agent Skill (cli/skills/**) must still
// exist in the live cobra command tree. A skill that references a renamed or
// removed flag/command is worse than no skill, so this fails CI on drift.
package skillparity

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Tencent/WeKnora/cli/cmd"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
)

var (
	codeFence = regexp.MustCompile("(?s)```[a-zA-Z]*\\n(.*?)```")
	longFlag  = regexp.MustCompile(`--[a-zA-Z][a-zA-Z0-9-]*`)
)

func skillsRoot(t *testing.T) string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "skills")
}

// allFlagNames collects every long flag name reachable in the command tree
// (each command's local + persistent + inherited flags, plus root persistent
// and the cobra-added help/version flags).
func allFlagNames(root *cobra.Command) map[string]bool {
	set := map[string]bool{"help": true, "version": true}
	add := func(fs *pflag.FlagSet) {
		fs.VisitAll(func(f *pflag.Flag) { set[f.Name] = true })
	}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		add(c.LocalFlags())
		add(c.PersistentFlags())
		add(c.InheritedFlags())
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	return set
}

// globalTakesValue reports, for each root persistent flag (long + short), whether
// it consumes the following token as a value (non-bool).
func globalTakesValue(root *cobra.Command) map[string]bool {
	m := map[string]bool{}
	root.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		takes := f.Value.Type() != "bool"
		m["--"+f.Name] = takes
		if f.Shorthand != "" {
			m["-"+f.Shorthand] = takes
		}
	})
	return m
}

func TestSkillsReferenceLiveCommandsAndFlags(t *testing.T) {
	root := cmd.NewRootCmd(&cmdutil.Factory{})
	flags := allFlagNames(root)
	globals := globalTakesValue(root)

	files := skillMarkdownFiles(t, skillsRoot(t))
	if len(files) == 0 {
		t.Fatal("no skill markdown files found under cli/skills/")
	}

	checkedAny := false
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		rel, _ := filepath.Rel(skillsRoot(t), path)
		for _, block := range codeFence.FindAllStringSubmatch(string(raw), -1) {
			for _, line := range strings.Split(block[1], "\n") {
				idx := strings.Index(line, "weknora ")
				if idx < 0 {
					continue
				}
				inv := line[idx+len("weknora"):]
				toks := strings.Fields(inv)
				if len(toks) == 0 {
					continue
				}
				// Skip leading global flags (and their value tokens).
				i := 0
				for i < len(toks) && strings.HasPrefix(toks[i], "-") {
					tok := toks[i]
					if eq := strings.Index(tok, "="); eq >= 0 {
						tok = tok[:eq]
					}
					i++
					if globals[tok] && i < len(toks) {
						i++ // consume the value
					}
				}
				// Placeholder command (e.g. `weknora <command> --help`) → skip.
				if i < len(toks) && strings.HasPrefix(toks[i], "<") {
					continue
				}
				// Greedily descend subcommands while the next token is one.
				curr := root
				for i < len(toks) {
					sub := findSub(curr, toks[i])
					if sub == nil {
						break
					}
					curr = sub
					i++
				}
				if curr != root {
					checkedAny = true
				}
				// Every long flag in the invocation must exist somewhere in the tree.
				for _, m := range longFlag.FindAllString(inv, -1) {
					name := strings.TrimPrefix(m, "--")
					if !flags[name] {
						t.Errorf("%s: skill references unknown flag --%s (renamed/removed? line: %q)", rel, name, strings.TrimSpace(line))
					}
				}
			}
		}
	}
	if !checkedAny {
		t.Fatal("parser resolved no weknora commands from skills — check tokenizer")
	}
}

func findSub(c *cobra.Command, name string) *cobra.Command {
	for _, sub := range c.Commands() {
		if sub.Name() == name {
			return sub
		}
		for _, a := range sub.Aliases {
			if a == name {
				return sub
			}
		}
	}
	return nil
}

func skillMarkdownFiles(t *testing.T, root string) []string {
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".md") {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}
