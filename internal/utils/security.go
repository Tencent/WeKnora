package utils

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"unicode/utf8"
)

// XSS 방어 관련 정규 표현식
var (
	// 잠재적인 XSS 공격 패턴 매칭
	xssPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)<script[^>]*>.*?</script>`),
		regexp.MustCompile(`(?i)<iframe[^>]*>.*?</iframe>`),
		regexp.MustCompile(`(?i)<object[^>]*>.*?</object>`),
		regexp.MustCompile(`(?i)<embed[^>]*>.*?</embed>`),
		regexp.MustCompile(`(?i)<embed[^>]*>`),
		regexp.MustCompile(`(?i)<form[^>]*>.*?</form>`),
		regexp.MustCompile(`(?i)<input[^>]*>`),
		regexp.MustCompile(`(?i)<button[^>]*>.*?</button>`),
		regexp.MustCompile(`(?i)javascript:`),
		regexp.MustCompile(`(?i)vbscript:`),
		regexp.MustCompile(`(?i)onload\s*=`),
		regexp.MustCompile(`(?i)onerror\s*=`),
		regexp.MustCompile(`(?i)onclick\s*=`),
		regexp.MustCompile(`(?i)onmouseover\s*=`),
		regexp.MustCompile(`(?i)onfocus\s*=`),
		regexp.MustCompile(`(?i)onblur\s*=`),
	}
)

// SanitizeHTML HTML 내용을 정리하여 XSS 공격 방지
func SanitizeHTML(input string) string {
	if input == "" {
		return ""
	}

	// 입력 길이 확인
	if len(input) > 10000 {
		input = input[:10000]
	}

	// 잠재적인 XSS 공격 확인
	for _, pattern := range xssPatterns {
		if pattern.MatchString(input) {
			// 악성 내용이 포함된 경우 HTML 이스케이프 처리
			return html.EscapeString(input)
		}
	}

	// 내용이 비교적 안전한 경우 원래 내용 반환
	return input
}

// EscapeHTML HTML 특수 문자 이스케이프
func EscapeHTML(input string) string {
	if input == "" {
		return ""
	}
	return html.EscapeString(input)
}

// ValidateInput 사용자 입력 검증
func ValidateInput(input string) (string, bool) {
	if input == "" {
		return "", true
	}

	// 제어 문자 포함 여부 확인
	for _, r := range input {
		if r < 32 && r != 9 && r != 10 && r != 13 {
			return "", false
		}
	}

	// UTF-8 유효성 확인
	if !utf8.ValidString(input) {
		return "", false
	}

	// 잠재적인 XSS 공격 확인
	for _, pattern := range xssPatterns {
		if pattern.MatchString(input) {
			return "", false
		}
	}

	return strings.TrimSpace(input), true
}

// IsValidURL URL이 안전한지 검증
func IsValidURL(url string) bool {
	if url == "" {
		return false
	}

	// 길이 확인
	if len(url) > 2048 {
		return false
	}

	// 프로토콜 확인
	if !strings.HasPrefix(strings.ToLower(url), "http://") &&
		!strings.HasPrefix(strings.ToLower(url), "https://") {
		return false
	}

	// 악성 내용 포함 여부 확인
	for _, pattern := range xssPatterns {
		if pattern.MatchString(url) {
			return false
		}
	}

	return true
}

// IsValidImageURL 이미지 URL이 안전한지 검증
func IsValidImageURL(url string) bool {
	if !IsValidURL(url) {
		return false
	}

	// 이미지 파일인지 확인
	imageExtensions := []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg", ".bmp", ".ico"}
	lowerURL := strings.ToLower(url)

	for _, ext := range imageExtensions {
		if strings.Contains(lowerURL, ext) {
			return true
		}
	}

	return false
}

// CleanMarkdown 마크다운 내용 정리
func CleanMarkdown(input string) string {
	if input == "" {
		return ""
	}

	// 잠재적인 악성 스크립트 제거
	cleaned := input
	for _, pattern := range xssPatterns {
		cleaned = pattern.ReplaceAllString(cleaned, "")
	}

	return cleaned
}

// SanitizeForDisplay 표시를 위해 내용 정리
func SanitizeForDisplay(input string) string {
	if input == "" {
		return ""
	}

	// 먼저 마크다운 정리
	cleaned := CleanMarkdown(input)

	// 그 다음 HTML 이스케이프 처리
	escaped := html.EscapeString(cleaned)

	return escaped
}

// SanitizeForLog 로그 입력을 정리하여 로그 주입 공격 방지
// 로그 주입 공격은 공격자가 입력에 줄 바꿈 및 기타 제어 문자를 삽입하여
// 로그 항목을 위조하는 것으로, 로그 분석 도구의 오판이나 악성 활동 은폐를 유발할 수 있음
func SanitizeForLog(input string) string {
	if input == "" {
		return ""
	}

	// 줄 바꿈(LF, CR, CRLF)을 공백으로 대체하여 로그 주입 방지
	sanitized := strings.ReplaceAll(input, "\n", " ")
	sanitized = strings.ReplaceAll(sanitized, "\r", " ")

	// 탭을 공백으로 대체
	sanitized = strings.ReplaceAll(sanitized, "\t", " ")

	// 기타 제어 문자 제거(ASCII 0-31, 공백은 이미 처리됨)
	var builder strings.Builder
	for _, r := range sanitized {
		// 인쇄 가능한 문자와 일반적인 유니코드 문자 유지
		if r >= 32 || r == ' ' {
			builder.WriteRune(r)
		}
	}

	sanitized = builder.String()

	return sanitized
}

// SanitizeForLogArray 로그 입력 배열을 정리하여 로그 주입 공격 방지
func SanitizeForLogArray(input []string) []string {
	if len(input) == 0 {
		return []string{}
	}

	sanitized := make([]string, 0, len(input))
	for _, item := range input {
		sanitized = append(sanitized, SanitizeForLog(item))
	}

	return sanitized
}

// AllowedStdioCommands defines the whitelist of allowed commands for MCP stdio transport
// These are the standard MCP server launchers that are considered safe
var AllowedStdioCommands = map[string]bool{
	"uvx": true, // Python package runner (uv)
	"npx": true, // Node.js package runner
}

// DangerousArgPatterns contains patterns that indicate potentially dangerous arguments
var DangerousArgPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^-c$`),                                   // Shell command execution flag
	regexp.MustCompile(`(?i)^--command$`),                            // Shell command execution flag
	regexp.MustCompile(`(?i)^-e$`),                                   // Eval flag
	regexp.MustCompile(`(?i)^--eval$`),                               // Eval flag
	regexp.MustCompile(`(?i)[;&|]`),                                  // Shell command chaining
	regexp.MustCompile(`(?i)\$\(`),                                   // Command substitution
	regexp.MustCompile("(?i)`"),                                      // Backtick command substitution
	regexp.MustCompile(`(?i)>\s*[/~]`),                               // Output redirection to absolute/home path
	regexp.MustCompile(`(?i)<\s*[/~]`),                               // Input redirection from absolute/home path
	regexp.MustCompile(`(?i)^/bin/`),                                 // Direct binary path
	regexp.MustCompile(`(?i)^/usr/bin/`),                             // Direct binary path
	regexp.MustCompile(`(?i)^/sbin/`),                                // Direct binary path
	regexp.MustCompile(`(?i)^/usr/sbin/`),                            // Direct binary path
	regexp.MustCompile(`(?i)^\.\./`),                                 // Path traversal
	regexp.MustCompile(`(?i)/\.\./`),                                 // Path traversal in middle
	regexp.MustCompile(`(?i)^(bash|sh|zsh|ksh|csh|tcsh|fish|dash)$`), // Shell interpreters as args
	regexp.MustCompile(`(?i)^(curl|wget|nc|netcat|ncat)$`),           // Network tools as args
	regexp.MustCompile(`(?i)^(rm|dd|mkfs|fdisk)$`),                   // Destructive commands as args
}

// DangerousEnvVarPatterns contains patterns for dangerous environment variable names or values
var DangerousEnvVarPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^LD_PRELOAD$`),      // Library injection
	regexp.MustCompile(`(?i)^LD_LIBRARY_PATH$`), // Library path manipulation
	regexp.MustCompile(`(?i)^DYLD_`),            // macOS dynamic linker
	regexp.MustCompile(`(?i)^PATH$`),            // PATH manipulation
	regexp.MustCompile(`(?i)^PYTHONPATH$`),      // Python path manipulation
	regexp.MustCompile(`(?i)^NODE_OPTIONS$`),    // Node.js options injection
	regexp.MustCompile(`(?i)^BASH_ENV$`),        // Bash environment file
	regexp.MustCompile(`(?i)^ENV$`),             // Shell environment file
	regexp.MustCompile(`(?i)^SHELL$`),           // Shell override
}

// ValidateStdioCommand validates the command for MCP stdio transport
// Returns an error if the command is not in the whitelist or contains dangerous patterns
func ValidateStdioCommand(command string) error {
	if command == "" {
		return fmt.Errorf("command cannot be empty")
	}

	// Normalize command (extract base name if it's a path)
	baseCommand := command
	if strings.Contains(command, "/") {
		parts := strings.Split(command, "/")
		baseCommand = parts[len(parts)-1]
	}

	// Check against whitelist
	if !AllowedStdioCommands[baseCommand] {
		return fmt.Errorf("command '%s' is not in the allowed list. Allowed commands: uvx, npx, node, python, python3, deno, bun", baseCommand)
	}

	// Additional check: command should not contain path traversal
	if strings.Contains(command, "..") {
		return fmt.Errorf("command path contains invalid characters")
	}

	return nil
}

// ValidateStdioArgs validates the arguments for MCP stdio transport
// Returns an error if any argument contains dangerous patterns
func ValidateStdioArgs(args []string) error {
	if len(args) == 0 {
		return nil
	}

	for i, arg := range args {
		// Check length
		if len(arg) > 1024 {
			return fmt.Errorf("argument %d exceeds maximum length (1024 characters)", i)
		}

		// Check against dangerous patterns
		for _, pattern := range DangerousArgPatterns {
			if pattern.MatchString(arg) {
				return fmt.Errorf("argument %d contains potentially dangerous pattern: %s", i, SanitizeForLog(arg))
			}
		}

		// Check for null bytes
		if strings.Contains(arg, "\x00") {
			return fmt.Errorf("argument %d contains null bytes", i)
		}
	}

	return nil
}

// ValidateStdioEnvVars validates environment variables for MCP stdio transport
// Returns an error if any env var name or value is dangerous
func ValidateStdioEnvVars(envVars map[string]string) error {
	if len(envVars) == 0 {
		return nil
	}

	for key, value := range envVars {
		// Check key against dangerous patterns
		for _, pattern := range DangerousEnvVarPatterns {
			if pattern.MatchString(key) {
				return fmt.Errorf("environment variable '%s' is not allowed for security reasons", key)
			}
		}

		// Check key length
		if len(key) > 256 {
			return fmt.Errorf("environment variable name '%s' exceeds maximum length", SanitizeForLog(key[:50]))
		}

		// Check value length
		if len(value) > 4096 {
			return fmt.Errorf("environment variable '%s' value exceeds maximum length", key)
		}

		// Check for null bytes in value
		if strings.Contains(value, "\x00") {
			return fmt.Errorf("environment variable '%s' value contains null bytes", key)
		}

		// Check value for shell injection patterns
		for _, pattern := range DangerousArgPatterns {
			if pattern.MatchString(value) {
				return fmt.Errorf("environment variable '%s' value contains potentially dangerous pattern", key)
			}
		}
	}

	return nil
}

// ValidateStdioConfig performs comprehensive validation of stdio configuration
// This should be called before creating or executing any stdio-based MCP client
func ValidateStdioConfig(command string, args []string, envVars map[string]string) error {
	// Validate command
	if err := ValidateStdioCommand(command); err != nil {
		return fmt.Errorf("invalid command: %w", err)
	}

	// Validate arguments
	if err := ValidateStdioArgs(args); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}

	// Validate environment variables
	if err := ValidateStdioEnvVars(envVars); err != nil {
		return fmt.Errorf("invalid environment variables: %w", err)
	}

	return nil
}
