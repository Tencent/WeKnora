// Command weknora is a CLI for Tencent WeKnora knowledge bases.
package main

import (
	"os"

	"github.com/Tencent/WeKnora/cli/cmd"
)

func main() {
	os.Exit(cmd.Execute())
}
