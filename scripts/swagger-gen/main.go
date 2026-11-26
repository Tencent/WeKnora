// Command swagger-gen generates docs/swagger.json and docs/swagger.yaml using
// the github.com/swaggo/swag version pinned in go.mod. docs/docs.go embeds
// swagger.json via //go:embed instead of duplicating a generated Go string.
package main

import (
	"flag"
	"io"
	"log"
	"os"

	"github.com/swaggo/swag"
	"github.com/swaggo/swag/gen"
)

func main() {
	outputDir := flag.String("o", "./docs", "output directory for swagger.json and swagger.yaml")
	quiet := flag.Bool("q", false, "suppress generator progress logs")
	flag.Parse()

	var logger swag.Debugger = log.New(os.Stdout, "", log.LstdFlags)
	if *quiet {
		logger = log.New(io.Discard, "", 0)
	}

	err := gen.New().Build(&gen.Config{
		SearchDir:          "./",
		Excludes:           "./internal/localsandbox",
		MainAPIFile:        "./cmd/server/main.go",
		PropNamingStrategy: swag.CamelCase,
		OutputDir:          *outputDir,
		OutputTypes:        []string{"json", "yaml"},
		ParseDependency:    1,
		ParseInternal:      true,
		ParseDepth:         100,
		OverridesFile:      "./docs/swag.overrides",
		ParseGoList:        true,
		LeftTemplateDelim:  "{{",
		RightTemplateDelim: "}}",
		CollectionFormat:   "csv",
		Debugger:           logger,
	})
	if err != nil {
		log.Fatalf("swagger generation failed: %v", err)
	}
}
