package browserskill

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRealExtension(t *testing.T) {
	extension := os.Getenv("BROWSERSKILL_TEST_EXTENSION")
	if extension == "" {
		t.Skip(
			"set BROWSERSKILL_TEST_EXTENSION, BROWSERSKILL_TEST_BINARY, " +
				"BROWSERSKILL_TEST_CHROMIUM and BROWSERSKILL_TEST_PLAYWRIGHT",
		)
	}
	m := NewManager(testStore(t))
	m.binary = os.Getenv("BROWSERSKILL_TEST_BINARY")
	server := httptest.NewServer(m)
	defer server.Close()
	t.Cleanup(m.Close)
	m.publicURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/extension"
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(
			w,
			`<!doctype html><title>BrowserSkill integration</title>
<label>Name <input id="name"></label>
<button
 onclick="document.querySelector('#result').textContent='你好 '+document.querySelector('#name').value">Save</button>
<p id="result"></p>`,
		)
	}))
	defer fixture.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	scope := Scope{1, "real-extension-test"}
	link, err := m.Pair(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	script, _ := filepath.Abs("../../scripts/test_browserskill_extension.mjs")
	cmd := exec.CommandContext(ctx, "node", script)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	hostLines := make(chan string, 8)
	go func() {
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			hostLines <- scanner.Text()
		}
		close(hostLines)
	}()
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	browserDone := make(chan error, 1)
	go func() { browserDone <- cmd.Wait() }()
	defer func() {
		_, _ = io.WriteString(input, "close\n")
		_ = input.Close()
		select {
		case <-browserDone:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
		}
	}()
	if err = json.NewEncoder(input).Encode(map[string]string{
		"pairing": link, "extension": extension,
		"chromium":   os.Getenv("BROWSERSKILL_TEST_CHROMIUM"),
		"playwright": os.Getenv("BROWSERSKILL_TEST_PLAYWRIGHT"),
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case line := <-hostLines:
		if line != "ready" {
			t.Fatalf("extension host not ready: %s", line)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	checkBackground := func() {
		t.Helper()
		_, err := io.WriteString(input, "check-background\n")
		if err != nil {
			t.Fatal(err)
		}
		select {
		case line := <-hostLines:
			if line != `{"background":true}` {
				t.Fatalf("browser stole focus: %s", line)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for !m.Status(scope, "chat").Connected {
		select {
		case err := <-browserDone:
			t.Fatalf("extension host exited: %v", err)
		case <-ctx.Done():
			t.Fatal("extension did not connect")
		case <-tick.C:
		}
	}
	if err = m.Control(ctx, scope, "chat", "select"); err != nil {
		t.Fatal(err)
	}
	if m.Status(scope, "chat").SessionID != "" {
		t.Fatal("source selection opened a task before the first browser call")
	}
	if _, err = m.Call(ctx, scope, "chat", "navigate", map[string]any{
		"url": fixture.URL, "wait_until": "domcontentloaded",
	}); err != nil {
		t.Fatal(err)
	}
	snap, err := m.Call(ctx, scope, "chat", "snapshot", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(snap), "Save") {
		t.Fatalf("page missing: %s", snap)
	}
	if _, err = m.Call(ctx, scope, "chat", "fill", map[string]any{"selector": "#name", "value": "世界"}); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Call(ctx, scope, "chat", "click", map[string]any{"selector": "button"}); err != nil {
		t.Fatal(err)
	}
	snap, err = m.Call(ctx, scope, "chat", "snapshot", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(snap), "你好 世界") {
		t.Fatalf("action result missing: %s", snap)
	}
	checkBackground()
	if _, err = m.Call(ctx, scope, "chat", "tab_create", map[string]any{"url": fixture.URL}); err != nil {
		t.Fatal(err)
	}
	checkBackground()
	if _, err = m.Preview(ctx, scope, "chat"); err != nil {
		t.Fatal(err)
	}
	checkBackground()
	// A human-help command stays pending while independent UI capture keeps working.
	helpDone := make(chan error, 1)
	go func() {
		_, e := m.Call(
			ctx,
			scope,
			"chat",
			"request_help",
			map[string]any{"prompt": "Integration test: no action required", "timeout_ms": 1500},
		)
		helpDone <- e
	}()
	for !m.Status(scope, "chat").NeedsHelp {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-tick.C:
		}
	}
	checkBackground()
	if _, err = m.Preview(ctx, scope, "chat"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-helpDone:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	checkBackground()
	if err = m.Focus(ctx, scope, "chat"); err != nil {
		t.Fatal(err)
	}
	frame, err := m.Preview(ctx, scope, "chat")
	if err != nil {
		t.Fatal(err)
	}
	var shot struct {
		Image  string `json:"image_base64"`
		Format string `json:"format"`
	}
	if json.Unmarshal(frame, &shot) != nil || shot.Format != "jpeg" {
		t.Fatal("invalid preview format")
	}
	raw, e := base64.StdEncoding.DecodeString(shot.Image)
	if e != nil {
		t.Fatal(e)
	}
	image, e := jpeg.Decode(bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	if image.Bounds().Dx() > 640 || image.Bounds().Dx() < 1 {
		t.Fatal("preview size is not bounded")
	}
	if out := os.Getenv("BROWSERSKILL_TEST_PREVIEW_FILE"); out != "" {
		if e = os.WriteFile(out, raw, 0o600); e != nil {
			t.Fatal(e)
		}
	}
	if err = m.Control(ctx, scope, "chat", "pause"); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Call(ctx, scope, "chat", "click", map[string]any{"selector": "button"}); err == nil {
		t.Fatal("paused click accepted")
	}
	if err = m.Control(ctx, scope, "chat", "resume"); err != nil {
		t.Fatal(err)
	}
	if err = m.Control(ctx, scope, "chat", "stop"); err != nil {
		t.Fatal(err)
	}
}
