package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Tencent/WeKnora/internal/database/migrationcheck"
)

func main() {
	root := flag.String("root", "migrations", "migration root containing versioned/ and mysql/")
	flag.Parse()

	if err := migrationcheck.CheckMySQLParity(*root); err != nil {
		fmt.Fprintf(os.Stderr, "migration parity check failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("migration parity check passed")
}
