// Command regression is the Benchmark v1.1 quality regression gate. It compares
// a current unified Benchmark result against a frozen baseline under a
// centralized threshold policy and exits with a CI-compatible status:
//
//	0 = PASS (all monitored metrics within thresholds)
//	1 = regression detected (or a metric is missing / non-finite)
//	2 = execution error (unreadable or malformed input)
//
// A quality regression can never produce exit code 0.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Tencent/WeKnora/internal/regression"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	exitPass       = 0
	exitRegression = 1
	exitError      = 2
)

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}

func runCLI(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("regression", flag.ContinueOnError)
	fs.SetOutput(stderr)
	baselinePath := fs.String("baseline", "", "path to the frozen baseline benchmark result JSON")
	currentPath := fs.String("current", "", "path to the current benchmark result JSON")
	policyPath := fs.String("policy", "", "optional path to a regression policy JSON (defaults to built-in)")
	reportPath := fs.String("report", "", "optional path to write the machine-readable JSON report")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	if *baselinePath == "" || *currentPath == "" {
		writeCLI(stderr, "regression: --baseline and --current are required\n")
		return exitError
	}

	policy := regression.DefaultPolicy()
	if *policyPath != "" {
		loaded, err := loadPolicy(*policyPath)
		if err != nil {
			writeCLI(stderr, "regression: %v\n", err)
			return exitError
		}
		policy = loaded
	}

	baseline, err := loadResult(*baselinePath)
	if err != nil {
		writeCLI(stderr, "regression: load baseline: %v\n", err)
		return exitError
	}
	current, err := loadResult(*currentPath)
	if err != nil {
		writeCLI(stderr, "regression: load current: %v\n", err)
		return exitError
	}

	// Contract guard first: a mismatch between the benchmark contracts is a
	// configuration failure (fail-closed), not a quality comparison.
	if mismatches := regression.CheckCompatibility(baseline, current); len(mismatches) > 0 {
		writeCLI(stdout, "%s", regression.FormatMismatches(mismatches))
		return exitRegression
	}

	report, err := regression.Compare(baseline, current, policy)
	if err != nil {
		writeCLI(stderr, "regression: %v\n", err)
		return exitError
	}

	writeCLI(stdout, "%s", report.RenderText())

	if *reportPath != "" {
		if err := writeReport(*reportPath, report); err != nil {
			writeCLI(stderr, "regression: %v\n", err)
			return exitError
		}
	}

	if report.OverallStatus == regression.StatusFail {
		return exitRegression
	}
	return exitPass
}

// writeCLI deliberately preserves the command's established exit-code contract
// when its caller-provided output stream fails.
func writeCLI(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func loadResult(path string) (*types.BenchmarkResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var result types.BenchmarkResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &result, nil
}

func loadPolicy(path string) (regression.Policy, error) {
	f, err := os.Open(path)
	if err != nil {
		return regression.Policy{}, err
	}
	defer func() { _ = f.Close() }()
	return regression.LoadPolicy(f)
}

func writeReport(path string, report *regression.Report) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
