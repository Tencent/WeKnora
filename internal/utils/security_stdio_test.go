package utils

import (
	"strings"
	"testing"
)

func TestValidateStdioCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command string
		wantErr bool
	}{
		{"empty command", "", true},
		{"allowed uvx", "uvx", false},
		{"allowed npx", "npx", false},
		{"allowed npx with path", "/usr/local/bin/npx", false},
		{"not allowed node", "node", true},
		{"not allowed python", "python", true},
		{"not allowed python3", "python3", true},
		{"not allowed deno", "deno", true},
		{"not allowed bun", "bun", true},
		{"not allowed bash", "bash", true},
		{"path traversal", "uvx/../../etc", true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateStdioCommand(tt.command)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateStdioCommand(%q) error = %v, wantErr %v", tt.command, err, tt.wantErr)
			}
		})
	}
}

// TestValidateStdioCommandErrorMessageMatchesWhitelist guards against the
// error message drifting from the actual AllowedStdioCommands whitelist. The
// message must list exactly the allowed commands and nothing else, so users
// are never told a command is permitted when it is in fact rejected.
func TestValidateStdioCommandErrorMessageMatchesWhitelist(t *testing.T) {
	t.Parallel()

	// Use a command that is clearly not part of the whitelist wording so the
	// "command '<name>' is not in..." prefix cannot collide with the list check.
	err := ValidateStdioCommand("node")
	if err == nil {
		t.Fatal("expected error for disallowed command 'node'")
	}

	msg := err.Error()

	// Isolate the "Allowed commands: ..." suffix, which is the part that must
	// match the whitelist exactly. The message prefix echoes the rejected
	// command, so checking the whole message would false-positive.
	const marker = "Allowed commands: "
	idx := strings.Index(msg, marker)
	if idx < 0 {
		t.Fatalf("error message %q is missing %q marker", msg, marker)
	}
	allowedList := msg[idx+len(marker):]

	if !strings.Contains(allowedList, "uvx") || !strings.Contains(allowedList, "npx") {
		t.Fatalf("allowed list %q should contain uvx and npx", allowedList)
	}

	// The advertised list must cover exactly the whitelist entries.
	for name := range AllowedStdioCommands {
		if !strings.Contains(allowedList, name) {
			t.Fatalf("allowed command %q is missing from the advertised list %q", name, allowedList)
		}
	}
	// A disallowed command must never be advertised as allowed.
	for _, name := range []string{"node", "python", "python3", "deno", "bun"} {
		if strings.Contains(allowedList, name) {
			t.Fatalf("advertised list %q contains disallowed command %q", allowedList, name)
		}
	}
}

func TestAllowedStdioCommandsString(t *testing.T) {
	t.Parallel()

	got := allowedStdioCommandsString()
	if got == "" {
		t.Fatal("allowedStdioCommandsString() returned empty string")
	}
	// Sorted, comma-separated, and covers every whitelist entry.
	names := strings.Split(got, ", ")
	if len(names) != len(AllowedStdioCommands) {
		t.Fatalf("allowedStdioCommandsString() = %q lists %d names, want %d", got, len(names), len(AllowedStdioCommands))
	}
	for _, name := range names {
		if !AllowedStdioCommands[name] {
			t.Fatalf("allowedStdioCommandsString() listed %q which is not in AllowedStdioCommands", name)
		}
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Fatalf("allowedStdioCommandsString() is not sorted: %v", names)
		}
	}
}
