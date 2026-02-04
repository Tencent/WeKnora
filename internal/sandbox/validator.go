package sandbox

import (
	"fmt"
	"regexp"
	"strings"
)

// ScriptValidator validates scripts and arguments for security
type ScriptValidator struct {
	// DangerousCommands are shell commands that should never be executed
	dangerousCommands []string
	// DangerousPatterns are regex patterns that indicate dangerous operations
	dangerousPatterns []*regexp.Regexp
	// ArgPatterns are regex patterns to detect injection in arguments
	argInjectionPatterns []*regexp.Regexp
}

// ValidationError represents a security validation failure
type ValidationError struct {
	Type    string // "dangerous_command", "dangerous_pattern", "arg_injection", "shell_injection"
	Pattern string // The pattern that matched
	Context string // Where it was found
	Message string // Human-readable description
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("security validation failed [%s]: %s (pattern: %s, context: %s)",
		e.Type, e.Message, e.Pattern, e.Context)
}

// ValidationResult contains all validation errors found
type ValidationResult struct {
	Valid  bool
	Errors []*ValidationError
}

// NewScriptValidator creates a new validator with default security rules
func NewScriptValidator() *ScriptValidator {
	v := &ScriptValidator{
		dangerousCommands: getDefaultDangerousCommands(),
	}
	v.dangerousPatterns = compilePatterns(getDefaultDangerousPatterns())
	v.argInjectionPatterns = compilePatterns(getDefaultArgInjectionPatterns())
	return v
}

// ValidateScript validates script content for dangerous patterns
func (v *ScriptValidator) ValidateScript(content string) *ValidationResult {
	result := &ValidationResult{Valid: true, Errors: make([]*ValidationError, 0)}

	// Check for dangerous commands (use simple string matching for complex patterns)
	for _, cmd := range v.dangerousCommands {
		if strings.Contains(content, cmd) {
			result.Valid = false
			result.Errors = append(result.Errors, &ValidationError{
				Type:    "dangerous_command",
				Pattern: cmd,
				Context: extractContext(content, cmd),
				Message: fmt.Sprintf("Script contains dangerous command: %s", cmd),
			})
		}
	}

	// Check for dangerous patterns (case-insensitive matching is already in patterns)
	lowerContent := strings.ToLower(content)
	for _, pattern := range v.dangerousPatterns {
		if matches := pattern.FindString(lowerContent); matches != "" {
			result.Valid = false
			result.Errors = append(result.Errors, &ValidationError{
				Type:    "dangerous_pattern",
				Pattern: pattern.String(),
				Context: extractContext(content, matches),
				Message: fmt.Sprintf("Script contains dangerous pattern: %s", matches),
			})
		}
	}

	// Check for network access attempts
	if v.hasNetworkAccess(content) {
		result.Valid = false
		result.Errors = append(result.Errors, &ValidationError{
			Type:    "network_access",
			Pattern: "network commands",
			Context: "script content",
			Message: "Script attempts to access network resources",
		})
	}

	// Check for reverse shell patterns
	if v.hasReverseShellPattern(content) {
		result.Valid = false
		result.Errors = append(result.Errors, &ValidationError{
			Type:    "reverse_shell",
			Pattern: "reverse shell pattern",
			Context: "script content",
			Message: "Script contains potential reverse shell pattern",
		})
	}

	return result
}

// ValidateArgs validates command-line arguments for injection attempts
func (v *ScriptValidator) ValidateArgs(args []string) *ValidationResult {
	result := &ValidationResult{Valid: true, Errors: make([]*ValidationError, 0)}

	for i, arg := range args {
		// Check for command chaining operators
		if v.hasShellOperators(arg) {
			result.Valid = false
			result.Errors = append(result.Errors, &ValidationError{
				Type:    "shell_injection",
				Pattern: "shell operators",
				Context: fmt.Sprintf("arg[%d]: %s", i, truncate(arg, 50)),
				Message: "Argument contains shell command operators",
			})
		}

		// Check for backtick/subshell command execution
		if v.hasCommandSubstitution(arg) {
			result.Valid = false
			result.Errors = append(result.Errors, &ValidationError{
				Type:    "command_substitution",
				Pattern: "command substitution",
				Context: fmt.Sprintf("arg[%d]: %s", i, truncate(arg, 50)),
				Message: "Argument contains command substitution syntax",
			})
		}

		// Check for injection patterns
		for _, pattern := range v.argInjectionPatterns {
			if pattern.MatchString(arg) {
				result.Valid = false
				result.Errors = append(result.Errors, &ValidationError{
					Type:    "arg_injection",
					Pattern: pattern.String(),
					Context: fmt.Sprintf("arg[%d]: %s", i, truncate(arg, 50)),
					Message: "Argument matches injection pattern",
				})
			}
		}
	}

	return result
}

// ValidateStdin validates stdin content for injection attempts
func (v *ScriptValidator) ValidateStdin(stdin string) *ValidationResult {
	result := &ValidationResult{Valid: true, Errors: make([]*ValidationError, 0)}

	// Check for embedded shell commands
	if v.hasEmbeddedShellCommands(stdin) {
		result.Valid = false
		result.Errors = append(result.Errors, &ValidationError{
			Type:    "stdin_injection",
			Pattern: "embedded shell commands",
			Context: truncate(stdin, 100),
			Message: "Stdin contains embedded shell command patterns",
		})
	}

	return result
}

// ValidateAll performs comprehensive validation on script, args, and stdin
func (v *ScriptValidator) ValidateAll(scriptContent string, args []string, stdin string) *ValidationResult {
	result := &ValidationResult{Valid: true, Errors: make([]*ValidationError, 0)}

	// Validate script content
	if scriptResult := v.ValidateScript(scriptContent); !scriptResult.Valid {
		result.Valid = false
		result.Errors = append(result.Errors, scriptResult.Errors...)
	}

	// Validate arguments
	if argsResult := v.ValidateArgs(args); !argsResult.Valid {
		result.Valid = false
		result.Errors = append(result.Errors, argsResult.Errors...)
	}

	// Validate stdin
	if stdin != "" {
		if stdinResult := v.ValidateStdin(stdin); !stdinResult.Valid {
			result.Valid = false
			result.Errors = append(result.Errors, stdinResult.Errors...)
		}
	}

	return result
}

// hasShellOperators checks for shell command chaining operators
func (v *ScriptValidator) hasShellOperators(s string) bool {
	// Shell operators that could be used for command chaining
	operators := []string{
		"&&", // AND operator
		"||", // OR operator
		";",  // Command separator
		"|",  // Pipe
		"\n", // Newline (can be used to inject commands)
		"\r", // Carriage return
		"$(", // Command substitution
		"`",  // Backtick command substitution
		">",  // Output redirection
		"<",  // Input redirection
		">>", // Append redirection
		"2>", // Stderr redirection
		"&>", // Combined redirection
	}

	for _, op := range operators {
		if strings.Contains(s, op) {
			return true
		}
	}
	return false
}

// hasCommandSubstitution checks for command substitution patterns
func (v *ScriptValidator) hasCommandSubstitution(s string) bool {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`\$\([^)]+\)`),   // $(command)
		regexp.MustCompile("`[^`]+`"),       // `command`
		regexp.MustCompile(`\$\{[^}]*\$\(`), // ${...$(command)
	}

	for _, p := range patterns {
		if p.MatchString(s) {
			return true
		}
	}
	return false
}

// hasNetworkAccess checks for network access patterns
func (v *ScriptValidator) hasNetworkAccess(content string) bool {
	patterns := []string{
		`\bcurl\b`,
		`\bwget\b`,
		`\bnc\b`,
		`\bnetcat\b`,
		`\btelnet\b`,
		`\bssh\b`,
		`\bscp\b`,
		`\brsync\b`,
		`\bftp\b`,
		`\bsftp\b`,
		`socket\.connect`,
		`urllib\.request`,
		`requests\.get`,
		`requests\.post`,
		`http\.client`,
		`httplib`,
		`fetch\s*\(`,
		`axios`,
		`XMLHttpRequest`,
	}

	for _, pattern := range patterns {
		if matched, _ := regexp.MatchString(`(?i)`+pattern, content); matched {
			return true
		}
	}
	return false
}

// hasReverseShellPattern checks for common reverse shell patterns
func (v *ScriptValidator) hasReverseShellPattern(content string) bool {
	patterns := []string{
		`/dev/tcp/`,
		`/dev/udp/`,
		`bash\s+-i`,
		`sh\s+-i`,
		`/bin/bash\s+-i`,
		`/bin/sh\s+-i`,
		`python.*pty\.spawn`,
		`perl.*-e.*socket`,
		`ruby.*-rsocket`,
		`socat.*exec`,
		`mkfifo`,
		`mknod.*p`,
		`0<&196`, // File descriptor redirection trick
		`196>&0`,
		`/inet/tcp/`,
		`bash.*>&.*0>&1`,
		`nc.*-e`,
		`ncat.*-e`,
		`netcat.*-e`,
	}

	for _, pattern := range patterns {
		if matched, _ := regexp.MatchString(`(?i)`+pattern, content); matched {
			return true
		}
	}
	return false
}

// hasEmbeddedShellCommands checks stdin for embedded shell commands
func (v *ScriptValidator) hasEmbeddedShellCommands(content string) bool {
	patterns := []string{
		`\$\(.*\)`,   // Command substitution
		"`.*`",       // Backtick substitution
		`\n\s*[;&|]`, // Newline followed by shell operators
		`\\n.*[;&|]`, // Escaped newline followed by shell operators
	}

	for _, pattern := range patterns {
		if matched, _ := regexp.MatchString(pattern, content); matched {
			return true
		}
	}
	return false
}

// getDefaultDangerousCommands returns commands that should not appear in scripts
func getDefaultDangerousCommands() []string {
	return []string{
		// System modification - various forms of dangerous rm
		"rm -rf /",
		"rm -fr /",
		"rm -rf /", // with different spacing
		"rm -rf/*",
		"rm -rf *",

		// Filesystem destruction
		"mkfs",
		"dd if=/dev/zero",
		"dd if=/dev/random",

		// Fork bombs (various forms)
		":(){ :|:& };:",
		":(){:|:&};:",
		"bomb(){ bomb|bomb& };bomb",

		// Process and system control
		"shutdown",
		"reboot",
		"halt",
		"poweroff",
		"init 0",
		"init 6",
		"killall",
		"pkill",

		// Permission escalation
		"chmod 777 /",
		"chown root",
		"setuid",
		"setgid",
		"passwd",

		// Credential access
		"/etc/passwd",
		"/etc/shadow",
		"/etc/sudoers",
		".ssh/",
		"id_rsa",
		"id_ed25519",

		// Environment manipulation
		"export PATH=",
		"export LD_PRELOAD",
		"export LD_LIBRARY_PATH",

		// Cron manipulation
		"crontab",
		"/etc/cron",

		// Service manipulation
		"systemctl",
		"service",

		// Module/kernel manipulation
		"insmod",
		"modprobe",
		"rmmod",

		// Container escape attempts
		"docker",
		"kubectl",
		"nsenter",
		"unshare",
		"capsh",
	}
}

// getDefaultDangerousPatterns returns regex patterns for dangerous operations
func getDefaultDangerousPatterns() []string {
	return []string{
		// Base64 encoded payloads (often used to hide malicious code)
		`base64\s+(-d|--decode)`,
		`echo\s+.*\|\s*base64\s+-d`,

		// Hex encoded payloads
		`xxd\s+-r`,
		`echo\s+-e\s+.*\\x`,

		// Code download and execution
		`curl.*\|\s*(bash|sh)`,
		`wget.*\|\s*(bash|sh)`,
		`python.*http\.server`,

		// Eval and exec patterns (code injection)
		`eval\s*\(`,
		`exec\s*\(`,
		`os\.system\s*\(`,
		`subprocess\.call\s*\(.*shell\s*=\s*True`,
		`subprocess\.Popen\s*\(.*shell\s*=\s*True`,
		`os\.popen\s*\(`,
		`commands\.getoutput\s*\(`,
		`commands\.getstatusoutput\s*\(`,

		// History/log manipulation
		`history\s+-c`,
		`unset\s+HISTFILE`,
		`export\s+HISTSIZE=0`,

		// Python dangerous functions
		`__import__\s*\(`,
		`importlib\.import_module`,
		`compile\s*\(.*exec`,

		// Pickle deserialization (can execute arbitrary code)
		`pickle\.loads?\s*\(`,
		`cPickle\.loads?\s*\(`,

		// YAML unsafe loading
		`yaml\.load\s*\([^,]+\)`, // Without Loader argument
		`yaml\.unsafe_load`,

		// Fork bomb patterns (function recursion with backgrounding)
		`:\s*\(\s*\)\s*\{\s*:`,           // :() { : pattern
		`\(\)\s*\{\s*\w+\s*\|\s*\w+\s*&`, // () { x | x & pattern

		// Dangerous rm patterns
		`rm\s+-[rf]+\s+/`, // rm -rf / or rm -fr /
		`rm\s+--no-preserve-root`,
	}
}

// getDefaultArgInjectionPatterns returns patterns for argument injection
func getDefaultArgInjectionPatterns() []string {
	return []string{
		// Path traversal
		`\.\.\/`,
		`\.\.\\`,

		// Environment variable injection
		`\$\{[A-Z_]+\}`,
		`\$[A-Z_]+`,

		// Special shell characters
		`\$\(`,
		"`",
		`\n`,
		`\r`,
	}
}

// compilePatterns compiles string patterns to regex
func compilePatterns(patterns []string) []*regexp.Regexp {
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		if r, err := regexp.Compile(`(?i)` + p); err == nil {
			compiled = append(compiled, r)
		}
	}
	return compiled
}

// extractContext extracts context around a match
func extractContext(content, match string) string {
	idx := strings.Index(strings.ToLower(content), strings.ToLower(match))
	if idx == -1 {
		return ""
	}

	start := idx - 20
	if start < 0 {
		start = 0
	}
	end := idx + len(match) + 20
	if end > len(content) {
		end = len(content)
	}

	context := content[start:end]
	if start > 0 {
		context = "..." + context
	}
	if end < len(content) {
		context = context + "..."
	}

	return context
}

// truncate truncates a string to max length
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
