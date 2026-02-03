//go:build ignore
// +build ignore

// Docker sandbox test program
// Usage: go run cmd/skills-demo/docker_test.go
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox"
)

func main() {
	fmt.Println("=" + strings.Repeat("=", 70))
	fmt.Println("  Docker Sandbox Test Suite")
	fmt.Println("=" + strings.Repeat("=", 70))
	fmt.Println()

	ctx := context.Background()

	// Get the path to examples/skills
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		fmt.Println("Error: Failed to get current file path")
		os.Exit(1)
	}
	scriptsDir := filepath.Join(filepath.Dir(filename), "..", "..", "examples", "skills", "pdf-processing", "scripts")

	// Create Docker sandbox
	config := sandbox.DefaultConfig()
	config.Type = sandbox.SandboxTypeDocker
	config.FallbackEnabled = false
	config.DockerImage = "python:3.11-slim"

	mgr, err := sandbox.NewManager(config)
	if err != nil {
		fmt.Printf("❌ Failed to create sandbox manager: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Sandbox Type: %s\n\n", mgr.GetType())

	// Test 1: Basic script execution
	fmt.Println("Test 1: Basic Script Execution")
	fmt.Println("-" + strings.Repeat("-", 50))
	runTest(ctx, mgr, &sandbox.ExecuteConfig{
		Script: filepath.Join(scriptsDir, "analyze_form.py"),
		Args:   []string{"sample.pdf"},
	})

	// Test 2: Script with different arguments
	fmt.Println("\nTest 2: Script with Different Arguments")
	fmt.Println("-" + strings.Repeat("-", 50))
	runTest(ctx, mgr, &sandbox.ExecuteConfig{
		Script: filepath.Join(scriptsDir, "extract_text.py"),
		Args:   []string{"document.pdf", "--page", "1"},
	})

	// Test 3: Environment variables
	fmt.Println("\nTest 3: Environment Variables")
	fmt.Println("-" + strings.Repeat("-", 50))
	runTest(ctx, mgr, &sandbox.ExecuteConfig{
		Script: filepath.Join(scriptsDir, "analyze_form.py"),
		Args:   []string{"test.pdf"},
		Env: map[string]string{
			"DEBUG":     "true",
			"LOG_LEVEL": "verbose",
		},
	})

	// Test 4: Network isolation (default: no network)
	fmt.Println("\nTest 4: Network Isolation (network disabled)")
	fmt.Println("-" + strings.Repeat("-", 50))
	runTest(ctx, mgr, &sandbox.ExecuteConfig{
		Script:       filepath.Join(scriptsDir, "analyze_form.py"),
		Args:         []string{"test.pdf"},
		AllowNetwork: false,
	})

	// Test 5: Memory limits
	fmt.Println("\nTest 5: Memory Limits (128MB)")
	fmt.Println("-" + strings.Repeat("-", 50))
	runTest(ctx, mgr, &sandbox.ExecuteConfig{
		Script:      filepath.Join(scriptsDir, "analyze_form.py"),
		Args:        []string{"test.pdf"},
		MemoryLimit: 128 * 1024 * 1024, // 128MB
	})

	// Test 6: CPU limits
	fmt.Println("\nTest 6: CPU Limits (0.5 cores)")
	fmt.Println("-" + strings.Repeat("-", 50))
	runTest(ctx, mgr, &sandbox.ExecuteConfig{
		Script:   filepath.Join(scriptsDir, "analyze_form.py"),
		Args:     []string{"test.pdf"},
		CPULimit: 0.5,
	})

	// Test 7: Read-only filesystem
	fmt.Println("\nTest 7: Read-only Root Filesystem")
	fmt.Println("-" + strings.Repeat("-", 50))
	runTest(ctx, mgr, &sandbox.ExecuteConfig{
		Script:         filepath.Join(scriptsDir, "analyze_form.py"),
		Args:           []string{"test.pdf"},
		ReadOnlyRootfs: true,
	})

	// Test 8: Custom timeout
	fmt.Println("\nTest 8: Custom Timeout (10s)")
	fmt.Println("-" + strings.Repeat("-", 50))
	runTest(ctx, mgr, &sandbox.ExecuteConfig{
		Script:  filepath.Join(scriptsDir, "analyze_form.py"),
		Args:    []string{"test.pdf"},
		Timeout: 10 * time.Second,
	})

	fmt.Println("\n" + strings.Repeat("=", 71))
	fmt.Println("  All Tests Completed!")
	fmt.Println(strings.Repeat("=", 71))
}

func runTest(ctx context.Context, mgr sandbox.Manager, config *sandbox.ExecuteConfig) {
	startTime := time.Now()
	result, err := mgr.Execute(ctx, config)
	totalTime := time.Since(startTime)

	if err != nil {
		fmt.Printf("❌ Error: %v\n", err)
		return
	}

	fmt.Printf("   Script: %s\n", filepath.Base(config.Script))
	fmt.Printf("   Args: %v\n", config.Args)
	fmt.Printf("   Exit Code: %d\n", result.ExitCode)
	fmt.Printf("   Duration: %v (total: %v)\n", result.Duration, totalTime)
	fmt.Printf("   Success: %v\n", result.IsSuccess())

	if result.Stdout != "" {
		// Show just the first 3 lines of output
		lines := strings.Split(result.Stdout, "\n")
		preview := strings.Join(lines[:min(3, len(lines))], "\n")
		fmt.Printf("   Output (preview):\n     %s\n", strings.ReplaceAll(preview, "\n", "\n     "))
	}

	if result.Stderr != "" {
		fmt.Printf("   Stderr: %s\n", strings.TrimSpace(result.Stderr))
	}

	if result.Error != "" {
		fmt.Printf("   Error: %s\n", result.Error)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
