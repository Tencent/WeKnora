// Command evaluation-reproduce emits deterministic regression artifacts for the golden dataset.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/evaluation/reproduce"
)

func main() { os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr)) }

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("evaluation-reproduce", flag.ContinueOnError)
	flags.SetOutput(stderr)
	outputDir := flags.String("output-dir", "artifacts/evaluation-regression", "report output directory")
	datasetPath := flags.String("dataset", "dataset/golden/v1/dataset.json", "golden dataset fixture")
	thresholdPath := flags.String(
		"thresholds", "evaluation/regression/thresholds.json", "versioned regression thresholds",
	)
	overridePath := flags.String("metric-overrides", "", "optional deterministic metric override fixture")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	commit, err := currentCommit(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "resolve git commit: %v\n", err)
		return 2
	}
	report, err := reproduce.Build(ctx, reproduce.BuildOptions{
		DatasetPath: *datasetPath, ThresholdPath: *thresholdPath, OverridePath: *overridePath,
		Commit: commit, Environment: reproduce.RuntimeEnvironment(),
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "build evaluation regression report: %v\n", err)
		return 2
	}
	jsonReport, err := reproduce.JSON(report)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "encode evaluation regression report: %v\n", err)
		return 2
	}
	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		_, _ = fmt.Fprintf(stderr, "create report directory: %v\n", err)
		return 2
	}
	jsonPath := filepath.Join(*outputDir, "evaluation-regression.json")
	markdownPath := filepath.Join(*outputDir, "evaluation-regression.md")
	if err := os.WriteFile(jsonPath, jsonReport, 0o644); err != nil {
		_, _ = fmt.Fprintf(stderr, "write JSON report: %v\n", err)
		return 2
	}
	if err := os.WriteFile(markdownPath, reproduce.Markdown(report), 0o644); err != nil {
		_, _ = fmt.Fprintf(stderr, "write Markdown report: %v\n", err)
		return 2
	}
	_, _ = fmt.Fprintf(stdout, "evaluation regression artifacts: %s, %s\n", jsonPath, markdownPath)
	if !report.Regression.Passed {
		for _, check := range report.Regression.Checks {
			if !check.Passed {
				_, _ = fmt.Fprintf(
					stderr,
					"regression: metric=%s baseline=%.12g current=%.12g "+
						"absolute_delta=%.12g threshold=%.12g\n",
					check.Metric, check.Baseline, check.Current,
					check.AbsoluteDelta, check.Threshold,
				)
			}
		}
		return 1
	}
	_, _ = fmt.Fprintln(stdout, "evaluation regression gate: PASS")
	return 0
}

func currentCommit(ctx context.Context) (string, error) {
	if commit := strings.TrimSpace(os.Getenv("GITHUB_SHA")); commit != "" {
		return commit, nil
	}
	command := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}
