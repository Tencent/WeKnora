// Command evalgate is the quality-gate CLI for evaluation runs.
//
// It reads a stored gate config (baseline + per-metric tolerance) and an
// evaluation result JSON, judges whether any gated metric regressed beyond
// tolerance, and exits non-zero when the gate fails — so it can gate a merge in
// CI. See scripts/eval-gate.sh for the full run → judge → report pipeline.
//
// Usage:
//
//	evalgate -config <gate.json> -result <result.json>
//	evalgate -config <gate.json> < result.json     # result from stdin
//
// Exit codes: 0 = gate passed, 1 = gate failed, 2 = usage/config/read error.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Tencent/WeKnora/internal/evalgate"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("evalgate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to gate config JSON (baseline + thresholds)")
	resultPath := fs.String("result", "", "path to evaluation result JSON (defaults to stdin)")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: evalgate -config <gate.json> [-result <result.json>]\n")
		fmt.Fprintf(stderr, "  Reads an evaluation result and judges it against a quality gate.\n")
		fmt.Fprintf(stderr, "  Exit 0 = pass, 1 = failed (regression), 2 = error.\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *configPath == "" {
		fmt.Fprintln(stderr, "evalgate: -config is required")
		fs.Usage()
		return 2
	}

	cfg, err := evalgate.LoadGateConfig(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "evalgate: %v\n", err)
		return 2
	}

	var raw []byte
	if *resultPath != "" {
		raw, err = os.ReadFile(*resultPath)
	} else {
		raw, err = io.ReadAll(stdin)
	}
	if err != nil {
		fmt.Fprintf(stderr, "evalgate: read result: %v\n", err)
		return 2
	}

	flat, err := evalgate.FlattenEvaluationResponse(raw)
	if err != nil {
		fmt.Fprintf(stderr, "evalgate: %v\n", err)
		return 2
	}

	report := evalgate.JudgeGate(flat, cfg)

	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "evalgate: marshal report: %v\n", err)
		return 2
	}
	fmt.Fprintln(stdout, string(out))
	if len(report.Errors) != 0 {
		return 2
	}

	if report.Passed {
		return 0
	}
	return 1
}
