package browserskill

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/large-page" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, "<!doctype html><title>Large task fixture</title><p>Ready</p>"+
				strings.Repeat(
					"<section><h2>Entry</h2><button>Open</button><p>Sample page content</p></section>", 2000))
			return
		}
		if r.URL.Path == "/slow-resource" {
			select {
			case <-time.After(1500 * time.Millisecond):
			case <-r.Context().Done():
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/slow-page" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w,
				`<title>Slow resource fixture</title><p>Content is ready</p><img src="/slow-resource">`)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(
			w,
			`<!doctype html><title>BrowserSkill integration</title>
<label>Name <input id="name"></label>
<button
 onclick="document.querySelector('#result').textContent='你好 '+document.querySelector('#name').value">Save</button>
<p id="result"></p>
<a id="new-tab" href="/linked" target="_blank" rel="noopener">Open linked page</a>
<button id="popup" onclick="window.open('/popup', '_blank')">Open popup</button>`,
		)
	}))
	defer fixture.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	scope := Scope{1, "real-extension-test"}
	call := func(
		ctx context.Context, scope Scope, session, method string, params map[string]any,
	) (json.RawMessage, error) {
		started := time.Now()
		result, err := m.Call(ctx, scope, session, method, params)
		t.Logf("browser RPC %s: %s (error=%v)", method, time.Since(started).Round(time.Millisecond), err)
		return result, err
	}
	link, err := m.Pair(ctx, scope, "")
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
	if _, err = call(ctx, scope, "chat", "navigate", map[string]any{
		"url": fixture.URL, "wait_until": "domcontentloaded",
	}); err != nil {
		t.Fatal(err)
	}
	snap, err := call(ctx, scope, "chat", "snapshot", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(snap), "Save") {
		t.Fatalf("page missing: %s", snap)
	}
	var snapshot struct {
		Text string `json:"text"`
	}
	if err = json.Unmarshal(snap, &snapshot); err != nil {
		t.Fatal(err)
	}
	ref := regexp.MustCompile(`@(e[0-9]+)\b`).FindStringSubmatch(snapshot.Text)
	if len(ref) != 2 {
		t.Fatalf("fixture has no screenshot crop ref: %s", snapshot.Text)
	}
	viewportWidth := 0
	for _, crop := range []string{"", ref[1]} {
		params := map[string]any{}
		if crop != "" {
			params["ref"] = crop
		}
		capture, captureErr := call(ctx, scope, "chat", "screenshot", params)
		if captureErr != nil {
			t.Fatal(captureErr)
		}
		var shot struct {
			Image string `json:"image_base64"`
		}
		if err = json.Unmarshal(capture, &shot); err != nil {
			t.Fatal(err)
		}
		data, decodeErr := base64.StdEncoding.DecodeString(shot.Image)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		img, decodeErr := png.Decode(bytes.NewReader(data))
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if crop == "" {
			viewportWidth = img.Bounds().Dx()
		} else if img.Bounds().Dx() >= viewportWidth {
			t.Fatal("element screenshot was not cropped")
		}
	}
	if _, err = call(ctx, scope, "chat", "fill", map[string]any{"selector": "#name", "value": "世界"}); err != nil {
		t.Fatal(err)
	}
	if _, err = call(ctx, scope, "chat", "click", map[string]any{"selector": "button"}); err != nil {
		t.Fatal(err)
	}
	snap, err = call(ctx, scope, "chat", "snapshot", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(snap), "你好 世界") {
		t.Fatalf("action result missing: %s", snap)
	}
	checkBackground()
	// Both target=_blank (including noopener) and window.open must become
	// background task-owned tabs, not leaked foreground user tabs.
	for i, selector := range []string{"#new-tab", "#popup"} {
		if i > 0 {
			if _, err = call(ctx, scope, "chat", "navigate", map[string]any{"url": fixture.URL}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = call(ctx, scope, "chat", "click", map[string]any{"selector": selector}); err != nil {
			t.Fatal(err)
		}
		listed, e := call(ctx, scope, "chat", "tab_list", map[string]any{"scope": "agent"})
		if e != nil {
			t.Fatal(e)
		}
		var result struct {
			Tabs []json.RawMessage `json:"tabs"`
		}
		if json.Unmarshal(listed, &result) != nil || len(result.Tabs) != i+2 {
			t.Fatalf("page-created tab was not claimed: %s", listed)
		}
		checkBackground()
	}
	if _, err = call(ctx, scope, "chat", "tab_create", map[string]any{"url": fixture.URL}); err != nil {
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
		_, e := call(
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
	if m.Status(scope, "chat").HelpPrompt != "Integration test: no action required" {
		t.Fatal("human-help prompt missing from preview status")
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
	if !m.Status(scope, "chat").Paused {
		t.Fatal("timed-out human help must retain a paused task")
	}
	if err = m.Control(ctx, scope, "chat", "resume"); err != nil {
		t.Fatal(err)
	}
	// Completing the real extension help overlay releases the same pending RPC.
	go func() {
		result, e := call(ctx, scope, "chat", "request_help", map[string]any{
			"prompt": "Confirm this fixture step", "timeout_ms": 10000,
		})
		if e == nil && !strings.Contains(string(result), `"continued"`) {
			e = fmt.Errorf("unexpected help outcome: %s", result)
		}
		helpDone <- e
	}()
	_, _ = io.WriteString(input, "complete-help\n")
	select {
	case line := <-hostLines:
		if line != "complete-help-done" {
			t.Fatalf("complete help: %s", line)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case e := <-helpDone:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if m.Status(scope, "chat").Paused || m.Status(scope, "chat").NeedsHelp {
		t.Fatal("completed help did not release automation")
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
	for _, waitUntil := range []string{"load", "default"} {
		t.Logf("navigation wait condition: %s", waitUntil)
		params := map[string]any{"url": fixture.URL + "/slow-page?" + waitUntil}
		if waitUntil != "default" {
			params["wait_until"] = waitUntil
		}
		result, navErr := call(ctx, scope, "chat", "navigate", params)
		if navErr != nil {
			t.Fatal(navErr)
		}
		if waitUntil == "default" && !strings.Contains(string(result), `"reached":"domcontentloaded"`) {
			t.Fatalf("default navigation did not stop at document ready: %s", result)
		}
	}
	taskID := m.Status(scope, "chat").SessionID
	if err = m.FinishTurn(ctx, scope, "chat", true); err != nil {
		t.Fatal(err)
	}
	if status := m.Status(scope, "chat"); !status.Idle || status.SessionID != taskID {
		t.Fatalf("idle lost task: %+v", status)
	}
	checkDetached := func() {
		t.Helper()
		_, _ = io.WriteString(input, "check-detached\n")
		select {
		case line := <-hostLines:
			if line != `{"detached":true}` {
				t.Fatalf("debugger still attached: %s", line)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	checkDetached()
	if _, err = m.Preview(ctx, scope, "chat"); err != nil {
		t.Fatal(err)
	}
	checkDetached()
	if _, err = call(ctx, scope, "chat", "snapshot", nil); err != nil {
		t.Fatal(err)
	}
	if m.Status(scope, "chat").Idle {
		t.Fatal("next operation did not resume control")
	}
	if err = m.Control(ctx, scope, "chat", "pause"); err != nil {
		t.Fatal(err)
	}
	if _, err = call(ctx, scope, "chat", "click", map[string]any{"selector": "button"}); err == nil {
		t.Fatal("paused click accepted")
	}
	if err = m.Control(ctx, scope, "chat", "resume"); err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(input, "interrupt-window\n")
	select {
	case line := <-hostLines:
		if line != "interrupt-window-done" {
			t.Fatalf("window interruption: %s", line)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for !m.Status(scope, "chat").Paused {
		select {
		case <-tick.C:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if err = m.Control(ctx, scope, "chat", "resume"); err != nil {
		t.Fatal(err)
	}
	if m.Status(scope, "chat").SessionID != taskID {
		t.Fatal("resume replaced the original session")
	}
	if _, err = call(ctx, scope, "chat", "navigate", map[string]any{"url": fixture.URL}); err != nil {
		t.Fatal(err)
	}
	// Focus above intentionally changes the foreground. Compare subsequent
	// background work with that user-selected tab, not the startup popup.
	_, _ = io.WriteString(input, "remember-foreground\n")
	select {
	case line := <-hostLines:
		if line != "remembered" {
			t.Fatalf("record foreground: %s", line)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// Both observations share the extension worker. A quadratic text renderer
	// can block the other conversation and the independent preview channel.
	for _, session := range []string{"chat", "parallel"} {
		if err = m.Control(ctx, scope, session, "select"); err != nil {
			t.Fatal(err)
		}
		if _, err = call(ctx, scope, session, "navigate",
			map[string]any{"url": fixture.URL + "/large-page"}); err != nil {
			t.Fatal(err)
		}
	}
	results := make(chan error, 3)
	for _, session := range []string{"chat", "parallel"} {
		go func(session string) {
			data, callErr := call(ctx, scope, session, "observe", nil)
			if callErr == nil && !strings.Contains(string(data), "Sample page content") {
				callErr = fmt.Errorf("large page observation is missing content")
			}
			results <- callErr
		}(session)
	}
	go func() { _, previewErr := m.Preview(ctx, scope, "chat"); results <- previewErr }()
	for range 3 {
		select {
		case err = <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	checkBackground()
	if err = m.FinishTurn(ctx, scope, "parallel", false); err != nil {
		t.Fatal(err)
	}
	if err = m.FinishTurn(ctx, scope, "chat", false); err != nil {
		t.Fatal(err)
	}
	checkDetached()
	checkCleanup := func() {
		t.Helper()
		_, _ = io.WriteString(input, "check-cleanup\n")
		select {
		case line := <-hostLines:
			if line != `{"cleaned":true}` {
				t.Fatalf("browser task leaked tabs or groups: %s", line)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	checkCleanup()
	// Repeated successful turns must return to the original user tabs, with
	// no live task groups left over. The next turn can start without Resume.
	for round := range 3 {
		t.Logf("automatic cleanup round %d", round+1)
		if err = m.Control(ctx, scope, "chat", "select"); err != nil {
			t.Fatal(err)
		}
		if _, err = call(ctx, scope, "chat", "navigate", map[string]any{"url": fixture.URL}); err != nil {
			t.Fatal(err)
		}
		if _, err = call(ctx, scope, "chat", "tab_create", map[string]any{"url": fixture.URL}); err != nil {
			t.Fatal(err)
		}
		if err = m.FinishTurn(ctx, scope, "chat", false); err != nil {
			t.Fatal(err)
		}
		checkCleanup()
	}
	// Stop must cancel a real pending extension command and clean its tabs,
	// rather than rejecting the control while the automation queue is busy.
	if err = m.Control(ctx, scope, "chat", "select"); err != nil {
		t.Fatal(err)
	}
	if _, err = call(ctx, scope, "chat", "navigate", map[string]any{"url": fixture.URL}); err != nil {
		t.Fatal(err)
	}
	go func() {
		_, callErr := call(ctx, scope, "chat", "request_help", map[string]any{
			"prompt": "This pending test will be cancelled by Stop", "timeout_ms": 30000,
		})
		helpDone <- callErr
	}()
	for !m.Status(scope, "chat").NeedsHelp {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-tick.C:
		}
	}
	if err = m.Control(ctx, scope, "chat", "stop"); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-helpDone:
		if err == nil {
			t.Fatal("Stop did not cancel pending help")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if m.Status(scope, "chat").Selected {
		t.Fatal("stopped task is still selected")
	}
	checkCleanup()
}
