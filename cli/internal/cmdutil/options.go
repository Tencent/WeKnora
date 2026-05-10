package cmdutil

import (
	"fmt"

	"github.com/spf13/cobra"
)

// MustRequireFlag panics on programmer error (typo in flag name). cobra's
// MarkFlagRequired only returns an error when the named flag does not exist
// on the command, which means the caller has a typo at registration time —
// non-recoverable. Wrap so command builders stay one-line.
func MustRequireFlag(cmd *cobra.Command, name string) {
	if err := cmd.MarkFlagRequired(name); err != nil {
		panic(fmt.Sprintf("MarkFlagRequired %q: %v", name, err))
	}
}
