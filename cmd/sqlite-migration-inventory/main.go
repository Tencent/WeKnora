package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Tencent/WeKnora/internal/database/sqlitemigrations"
)

func main() {
	directory := flag.String("directory", "", "SQLite migrations directory")
	flag.Parse()

	inventory, err := sqlitemigrations.ValidateDirectory(
		*directory,
		sqlitemigrations.RequiredVersion,
	)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "sqlite migration inventory: %v\n", err)
		os.Exit(1)
	}
	_, _ = fmt.Fprintln(os.Stdout, strings.Join(inventory.Files, "\n"))
}
