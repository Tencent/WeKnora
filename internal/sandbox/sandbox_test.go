package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	if config.Type != SandboxTypeLocal {
		t.Errorf("Expected default type to be local, got %s", config.Type)
	}

	if config.DefaultTimeout != DefaultTimeout {
		t.Errorf("Expected default timeout %v, got %v", DefaultTimeout, config.DefaultTimeout)
	}

	if !config.FallbackEnabled {
		t.Error("Expected fallback to be enabled by default")
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
	}{
		{
			name:    "nil config",
			config:  nil,
			wantErr: true,
		},
		{
			name: "valid config",
			config: &Config{
				Type:           SandboxTypeLocal,
				DefaultTimeout: 30 * time.Second,
			},
			wantErr: false,
		},
		{
			name: "invalid type",
			config: &Config{
				Type: "invalid",
			},
			wantErr: true,
		},
		{
			name: "negative timeout",
			config: &Config{
				Type:           SandboxTypeLocal,
				DefaultTimeout: -1 * time.Second,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConfig(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLocalSandboxExecute(t *testing.T) {
	// Create a temporary script
	tmpDir, err := os.MkdirTemp("", "sandbox-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Write a simple test script
	scriptPath := filepath.Join(tmpDir, "test.sh")
	scriptContent := `#!/bin/bash
echo "Hello from sandbox"
echo "Args: $@"
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	// Create local sandbox
	config := DefaultConfig()
	config.Type = SandboxTypeLocal
	sandbox := NewLocalSandbox(config)

	// Check availability
	ctx := context.Background()
	if !sandbox.IsAvailable(ctx) {
		t.Error("Local sandbox should always be available")
	}

	// Execute script
	result, err := sandbox.Execute(ctx, &ExecuteConfig{
		Script:  scriptPath,
		Args:    []string{"arg1", "arg2"},
		Timeout: 10 * time.Second,
	})

	if err != nil {
		t.Fatalf("Failed to execute script: %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", result.ExitCode)
	}

	if result.Stdout == "" {
		t.Error("Expected stdout to be non-empty")
	}

	t.Logf("Script output: %s", result.Stdout)
	t.Logf("Duration: %v", result.Duration)
}

func TestLocalSandboxTimeout(t *testing.T) {
	// Create a temporary script that sleeps
	tmpDir, err := os.MkdirTemp("", "sandbox-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Write a script that sleeps
	scriptPath := filepath.Join(tmpDir, "sleep.sh")
	scriptContent := `#!/bin/bash
sleep 10
echo "Done"
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	// Create local sandbox
	config := DefaultConfig()
	config.Type = SandboxTypeLocal
	sandbox := NewLocalSandbox(config)

	// Execute with short timeout
	ctx := context.Background()
	result, err := sandbox.Execute(ctx, &ExecuteConfig{
		Script:  scriptPath,
		Timeout: 1 * time.Second,
	})

	if err != nil {
		t.Fatalf("Execute should not return error, got: %v", err)
	}

	if !result.Killed {
		t.Error("Expected script to be killed due to timeout")
	}

	t.Logf("Script was killed: %v, Duration: %v", result.Killed, result.Duration)
}

func TestNewManager(t *testing.T) {
	config := DefaultConfig()
	config.Type = SandboxTypeLocal

	manager, err := NewManager(config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	if manager.GetType() != SandboxTypeLocal {
		t.Errorf("Expected type local, got %s", manager.GetType())
	}
}

func TestNewDisabledManager(t *testing.T) {
	manager := NewDisabledManager()

	if manager.GetType() != SandboxTypeDisabled {
		t.Errorf("Expected type disabled, got %s", manager.GetType())
	}

	// Execute should fail
	ctx := context.Background()
	_, err := manager.Execute(ctx, &ExecuteConfig{
		Script: "/some/script.sh",
	})

	if err != ErrSandboxDisabled {
		t.Errorf("Expected ErrSandboxDisabled, got %v", err)
	}
}

func TestExecuteResultHelpers(t *testing.T) {
	// Test IsSuccess
	successResult := &ExecuteResult{
		ExitCode: 0,
		Stdout:   "output",
	}
	if !successResult.IsSuccess() {
		t.Error("Expected IsSuccess() to return true for exit code 0")
	}

	failResult := &ExecuteResult{
		ExitCode: 1,
		Stderr:   "error",
	}
	if failResult.IsSuccess() {
		t.Error("Expected IsSuccess() to return false for exit code 1")
	}

	killedResult := &ExecuteResult{
		ExitCode: 0,
		Killed:   true,
	}
	if killedResult.IsSuccess() {
		t.Error("Expected IsSuccess() to return false when killed")
	}

	// Test GetOutput
	if successResult.GetOutput() != "output" {
		t.Errorf("Expected GetOutput() to return stdout, got %s", successResult.GetOutput())
	}

	if failResult.GetOutput() != "error" {
		t.Errorf("Expected GetOutput() to return stderr when stdout is empty, got %s", failResult.GetOutput())
	}
}

func TestPythonScriptExecution(t *testing.T) {
	// Create a temporary Python script
	tmpDir, err := os.MkdirTemp("", "sandbox-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Write a Python script
	scriptPath := filepath.Join(tmpDir, "test.py")
	scriptContent := `#!/usr/bin/env python3
import sys
print("Hello from Python")
print(f"Arguments: {sys.argv[1:]}")
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	// Create local sandbox
	config := DefaultConfig()
	config.Type = SandboxTypeLocal
	sandbox := NewLocalSandbox(config)

	// Execute Python script
	ctx := context.Background()
	result, err := sandbox.Execute(ctx, &ExecuteConfig{
		Script:  scriptPath,
		Args:    []string{"test", "args"},
		Timeout: 10 * time.Second,
	})

	if err != nil {
		t.Fatalf("Failed to execute Python script: %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("Expected exit code 0, got %d. Stderr: %s", result.ExitCode, result.Stderr)
	}

	t.Logf("Python script output: %s", result.Stdout)
}
