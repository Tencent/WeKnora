package sandbox

import (
	"context"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	if config.Type != SandboxTypeDisabled {
		t.Errorf("Expected default type to be disabled, got %s", config.Type)
	}

	if config.DefaultTimeout != DefaultTimeout {
		t.Errorf("Expected default timeout %v, got %v", DefaultTimeout, config.DefaultTimeout)
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
			name: "valid disabled config",
			config: &Config{
				Type:           SandboxTypeDisabled,
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
			name: "removed local type",
			config: &Config{
				Type: "local",
			},
			wantErr: true,
		},
		{
			name: "negative timeout",
			config: &Config{
				Type:           SandboxTypeDisabled,
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

func TestNewManager(t *testing.T) {
	config := DefaultConfig()
	config.Type = SandboxTypeDisabled

	manager, err := NewManager(config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	if manager.GetType() != SandboxTypeDisabled {
		t.Errorf("Expected type disabled, got %s", manager.GetType())
	}
}

func TestNewDisabledManager(t *testing.T) {
	manager := NewDisabledManager()

	if manager.GetType() != SandboxTypeDisabled {
		t.Errorf("Expected type disabled, got %s", manager.GetType())
	}

	ctx := context.Background()
	_, err := manager.Execute(ctx, &ExecuteConfig{
		Script: "/some/script.sh",
	})

	if err != ErrSandboxDisabled {
		t.Errorf("Expected ErrSandboxDisabled, got %v", err)
	}
}

func TestIsNamedSandboxBackendType(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{"cube", true},
		{"e2b", true},
		{"docker", true},
		{"local", false},
		{"disabled", false},
		{"", false},
	} {
		if got := IsNamedSandboxBackendType(tc.raw); got != tc.want {
			t.Fatalf("IsNamedSandboxBackendType(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestExecuteResultHelpers(t *testing.T) {
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
}
