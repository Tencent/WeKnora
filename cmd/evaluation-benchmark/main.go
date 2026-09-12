// Command evaluation-benchmark measures the isolated evaluation pipeline matrix.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/evaluation/performance"
)

func main() {
	flags := flag.NewFlagSet("evaluation-benchmark", flag.ExitOnError)
	outputDir := flags.String("output-dir", "artifacts/evaluation-performance", "report output directory")
	datasetPath := flags.String("dataset", "dataset/golden/v1/dataset.json", "golden dataset fixture")
	if err := flags.Parse(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := run(context.Background(), *datasetPath, *outputDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, datasetPath, outputDir string) error {
	commit, err := currentCommit(ctx)
	if err != nil {
		return fmt.Errorf("resolve git commit: %w", err)
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return err
	}
	options := performance.DefaultOptions()
	options.DatasetPath, options.Commit, options.OutputRoot = datasetPath, commit, outputDir
	report, err := performance.Build(ctx, options)
	if err != nil {
		return fmt.Errorf("run evaluation benchmark: %w", err)
	}
	jsonReport, err := performance.JSON(report)
	if err != nil {
		return err
	}
	jsonPath := filepath.Join(outputDir, "evaluation-performance.json")
	markdownPath := filepath.Join(outputDir, "evaluation-performance.md")
	if err := os.WriteFile(jsonPath, jsonReport, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(markdownPath, performance.Markdown(report), 0o644); err != nil {
		return err
	}
	fmt.Printf("evaluation performance artifacts: %s, %s\n", jsonPath, markdownPath)
	return nil
}

func currentCommit(ctx context.Context) (string, error) {
	if commit := strings.TrimSpace(os.Getenv("GITHUB_SHA")); commit != "" {
		return commit, nil
	}
	output, err := exec.CommandContext(ctx, "git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}
