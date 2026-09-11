package session

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func terminalAuditTestMarker(token string, exitCode int, historyLine string) []byte {
	encoded := base64.StdEncoding.EncodeToString([]byte(historyLine))
	return []byte(terminalAuditMarkerLead + token + ";" + strconv.Itoa(exitCode) + ";" + encoded + "\a")
}

func TestTerminalAuditMarkerFilterHandlesEveryChunkBoundary(t *testing.T) {
	token := "0123456789abcdef"
	marker := terminalAuditTestMarker(token, 1, "  42  false")
	stream := append([]byte("\x1b[32m前\x1b[0m"), marker...)
	stream = append(stream, []byte("后\r\n")...)
	wantOutput := []byte("\x1b[32m前\x1b[0m后\r\n")

	for split := 0; split <= len(stream); split++ {
		var gotExit int
		var gotCommand string
		filter := newTerminalAuditMarkerFilter(token, func(exitCode int, command string) {
			gotExit, gotCommand = exitCode, command
		})
		got := append([]byte(nil), filter.Consume(stream[:split])...)
		got = append(got, filter.Consume(stream[split:])...)
		got = append(got, filter.Flush()...)
		if !bytes.Equal(got, wantOutput) || gotExit != 1 || gotCommand != "false" {
			t.Fatalf("split=%d output=%q exit=%d command=%q", split, got, gotExit, gotCommand)
		}
	}
}

func TestTerminalAuditMarkerFilterRejectsWrongOrMalformedMarkers(t *testing.T) {
	validToken := "valid-token"
	wrong := terminalAuditTestMarker("wrong-token", 0, "  1  echo visible")
	called := false
	filter := newTerminalAuditMarkerFilter(validToken, func(int, string) { called = true })
	got := append(filter.Consume(wrong), filter.Flush()...)
	if !bytes.Equal(got, wrong) || called {
		t.Fatalf("wrong-token marker changed=%t called=%t", !bytes.Equal(got, wrong), called)
	}

	malformed := []byte(terminalAuditMarkerLead + validToken + ";0;not-base64\a")
	filter = newTerminalAuditMarkerFilter(validToken, func(int, string) { called = true })
	got = append(filter.Consume(malformed), filter.Flush()...)
	if len(got) != 0 || called {
		t.Fatalf("authenticated malformed marker output=%q called=%t", got, called)
	}
}

func TestSanitizeTerminalAuditCommandRedactsSecretsAndBoundsUTF8(t *testing.T) {
	raw := "  77  export API_TOKEN=topsecret; curl --password 'hunter2' " +
		`-H "Authorization: Bearer ey.secret.token" -H 'X-API-Key: header-secret' ` +
		"-u alice:user-secret https://alice:pw@example.test/path \\\n" +
		"--client-secret multiline-secret \\\n-pmultiline-password" +
		"\x00\x01"
	got := sanitizeTerminalAuditCommand(raw)
	for _, secret := range []string{
		"topsecret", "hunter2", "ey.secret.token", "header-secret", "user-secret", ":pw@",
		"multiline-secret", "multiline-password",
	} {
		if strings.Contains(got, secret) {
			t.Fatalf("sanitized command leaked %q: %q", secret, got)
		}
	}
	if strings.Count(got, "[REDACTED]") < 8 {
		t.Fatalf("sanitized command did not redact every secret form: %q", got)
	}
	if strings.ContainsAny(got, "\x00\x01") {
		t.Fatalf("sanitized command retained control bytes: %q", got)
	}

	long := strings.Repeat("界", terminalAuditMaxCommandBytes)
	bounded := sanitizeTerminalAuditCommand("  1  " + long)
	if len(bounded) > terminalAuditMaxCommandBytes || !strings.HasSuffix(bounded, "…") {
		t.Fatalf("bounded command bytes=%d suffix=%q", len(bounded), bounded[len(bounded)-3:])
	}
}

// The keyword anchor must not require a leading name or a flag name that
// starts with the keyword: `PASSWORD=` and `--http-password=` are at least as
// common as `API_TOKEN=` and `--password=`, and both leaked before.
func TestSanitizeTerminalAuditCommandRedactsBareAndPrefixedNames(t *testing.T) {
	cases := []struct {
		command string
		secrets []string
	}{
		{"export PASSWORD=hunter2", []string{"hunter2"}},
		{"PASSWORD=hunter2", []string{"hunter2"}},
		{"TOKEN=ghp_abc123", []string{"ghp_abc123"}},
		{"SECRET=abc123", []string{"abc123"}},
		{"API_KEY=xyz", []string{"xyz"}},
		{"CREDENTIAL=x", []string{"x"}},
		{"PASSPHRASE=letmein", []string{"letmein"}},
		{"wget --http-password=hunter2 https://x", []string{"hunter2"}},
		{"--db-token=abc123", []string{"abc123"}},
		// Non-bearer schemes are just as secret as bearer ones.
		{`curl -H "Authorization: token ghp_abc123" https://api.github.com`, []string{"ghp_abc123"}},
		{`curl -H "Authorization: ApiKey sekret" https://x`, []string{"sekret"}},
		// A quoted header value must not disable the match entirely.
		{`curl -H 'X-Api-Key: "sekret"' https://x`, []string{"sekret"}},
		{`curl -H 'Authorization: Bearer "sekret"' https://x`, []string{"sekret"}},
		{`curl -H 'X-Auth-Token: "sekret"' https://x`, []string{"sekret"}},
	}
	for _, tc := range cases {
		got := sanitizeTerminalAuditCommand(tc.command)
		for _, secret := range tc.secrets {
			if strings.Contains(got, secret) {
				t.Fatalf("sanitized command leaked %q: %q -> %q", secret, tc.command, got)
			}
		}
	}

	// Widening the keyword anchors must not redact ordinary commands.
	for _, command := range []string{
		"echo hello world",
		"cd $(pwd) && ls -la",
		"git log --oneline -p",
		"python Build_Report.py",
		"grep -n 'foo=' file.txt",
	} {
		if got := sanitizeTerminalAuditCommand(command); got != command {
			t.Fatalf("ordinary command was rewritten: %q -> %q", command, got)
		}
	}
}

type blockingTerminalAuditService struct {
	interfaces.AuditLogService
	started chan struct{}
	release chan struct{}
}

func (s *blockingTerminalAuditService) Log(context.Context, *types.AuditLog) error {
	close(s.started)
	<-s.release
	return nil
}

func TestTerminalAuditRecorderCloseRemainsBoundedWhenStoreIgnoresContext(t *testing.T) {
	service := &blockingTerminalAuditService{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	recorder := newTerminalAuditRecorder(
		context.Background(), service, 7, "user-1", "member", "session-1", "cube", 41,
	)
	recorder.closeWait = 20 * time.Millisecond
	recorder.Record(0, "echo ok")
	<-service.started

	started := time.Now()
	recorder.Close()
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("Close exceeded its bound: %v", elapsed)
	}
	close(service.release)
	select {
	case <-recorder.done:
	case <-time.After(time.Second):
		t.Fatal("recorder worker did not stop after the blocked store returned")
	}
}

type terminalAuditServiceCapture struct {
	interfaces.AuditLogService
	entries []*types.AuditLog
}

func (c *terminalAuditServiceCapture) Log(_ context.Context, entry *types.AuditLog) error {
	copyEntry := *entry
	copyEntry.Details = append(types.JSON(nil), entry.Details...)
	c.entries = append(c.entries, &copyEntry)
	return nil
}

func TestTerminalAuditRecorderWritesNativeRowsWithExitOutcome(t *testing.T) {
	capture := &terminalAuditServiceCapture{}
	recorder := newTerminalAuditRecorder(
		context.Background(), capture, 7, "user-1", "member", "session-1", "cube", 41,
	)
	recorder.Record(0, "echo ok")
	recorder.Record(9, "false")
	recorder.Close()

	if len(capture.entries) != 2 {
		t.Fatalf("entries=%d want=2", len(capture.entries))
	}
	if capture.entries[0].Action != types.AuditActionSandboxTerminalCommand ||
		capture.entries[0].Outcome != types.AuditOutcomeSuccess ||
		capture.entries[1].Outcome != types.AuditOutcomeFailed {
		t.Fatalf("unexpected outcomes: %+v %+v", capture.entries[0], capture.entries[1])
	}
	if capture.entries[0].ScopeType != "" || capture.entries[0].TargetType != "sandbox_session" ||
		capture.entries[0].TargetID != "session-1" {
		t.Fatalf("unexpected scope/target: %+v", capture.entries[0])
	}
	var details map[string]any
	if err := json.Unmarshal(capture.entries[1].Details, &details); err != nil {
		t.Fatal(err)
	}
	if details["command"] != "false" || details["exit_code"] != float64(9) || details["backend"] != "cube" {
		t.Fatalf("unexpected details: %#v", details)
	}
}

func TestTerminalAuditEnvironmentDoesNotInstallWithoutToken(t *testing.T) {
	if got := terminalAuditEnvironment(""); got != nil {
		t.Fatalf("empty token installed audit env: %#v", got)
	}
	env := terminalAuditEnvironment("token")
	if env["PROMPT_COMMAND"] == "" || env["WEKNORA_AUDIT_TOKEN"] != "token" {
		t.Fatalf("invalid audit env: %#v", env)
	}
}
