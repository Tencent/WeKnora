package cmd

import (
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
)

// dryRunExpectation declares, for every leaf command, whether it must register
// --dry-run. The rule (per internal/cmdutil/dryrun.go): commands that change
// server/local state get a preview path. Reads, generate/stream ops, the
// interactive credential flow, and the long-running MCP server are exempt.
//
// This is a drift guard (sibling of TestEveryLeafCommandHasAgentHelp): every
// leaf must appear here, so a newly-added command forces an explicit dry-run
// decision rather than silently inheriting "no preview".
var dryRunExpectation = map[string]bool{
	// --- mutations: MUST have --dry-run ---
	"kb create": true, "kb edit": true, "kb delete": true, "kb pin": true, "kb unpin": true,
	"doc create": true, "doc upload": true, "doc fetch": true, "doc delete": true,
	"chunk delete":   true,
	"session delete": true, "session stop": true,
	"agent create": true, "agent edit": true, "agent delete": true,
	"profile add": true, "profile use": true, "profile remove": true,
	"auth logout": true, "auth refresh": true,
	"link": true, "unlink": true,
	"api": true, // passthrough: dry-run previews write methods, rejected on GET

	// --- exempt: no state change to preview ---
	// reads
	"kb list": false, "kb view": false, "kb status": false, "kb check": false,
	"doc list": false, "doc view": false, "doc download": false,
	"doc wait":   false, // polling read, no mutation
	"chunk list": false, "chunk view": false,
	"session list": false, "session view": false,
	"agent list": false, "agent view": false, "agent status": false, "agent check": false,
	"search chunks": false, "search docs": false, "search kb": false, "search sessions": false,
	"auth list": false, "auth status": false, "auth token": false,
	"profile list": false,
	"doctor":       false, "version": false,
	// generate / stream ops — the session-creation side effect is incidental,
	// not a CRUD write; a no-SDK-call preview would be meaningless.
	"chat": false, "session ask": false, "session continue-stream": false,
	// auth login VALIDATES credentials against the server and stores them; its
	// whole purpose is the server round-trip, which a side-effect-free dry-run
	// cannot exercise — so previewing it would be misleading. Exempt by design.
	"auth login": false,
	// long-running stdio server, not a one-shot command.
	"mcp serve": false,
}

func TestDryRunCoverageMatchesExpectation(t *testing.T) {
	root := NewRootCmd(cmdutil.New())

	var unlisted, wrong []string
	eachLeafCommand(root, func(c *cobra.Command) {
		key := strings.TrimPrefix(c.CommandPath(), "weknora ")
		want, declared := dryRunExpectation[key]
		if !declared {
			unlisted = append(unlisted, key)
			return
		}
		has := c.Flags().Lookup("dry-run") != nil
		if has != want {
			wrong = append(wrong, key)
		}
	})

	sort.Strings(unlisted)
	sort.Strings(wrong)
	if len(unlisted) > 0 {
		t.Errorf("leaf commands missing from dryRunExpectation (declare must-have-dry-run true/false):\n  %v", unlisted)
	}
	if len(wrong) > 0 {
		t.Errorf("dry-run flag presence disagrees with dryRunExpectation:\n  %v", wrong)
	}
}
