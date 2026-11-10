package cmd

import "github.com/spf13/cobra"

// eachLeafCommand invokes fn for every runnable leaf command (no real
// subcommands) in the tree rooted at root, skipping cobra's generated
// `completion`/`help` subtrees and hidden commands. Shared by the agent-help
// and dry-run coverage drift guards so they agree, byte-for-byte, on what
// counts as a leaf.
func eachLeafCommand(root *cobra.Command, fn func(*cobra.Command)) {
	exempt := map[string]bool{"completion": true, "help": true}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		realKids := 0
		for _, k := range c.Commands() {
			if exempt[k.Name()] || k.Hidden {
				continue
			}
			realKids++
			walk(k)
		}
		if c == root || realKids > 0 || exempt[c.Name()] {
			return // group command or exempt — not a leaf
		}
		fn(c)
	}
	walk(root)
}
