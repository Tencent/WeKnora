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
		if r.URL.Path == "/slow-error-page" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, "<!doctype html><title>Fixture slow error</title>"+
				"<p>Visible error while response is loading</p>")
			w.(http.Flusher).Flush()
			select {
			case <-time.After(time.Second):
			case <-r.Context().Done():
			}
			return
		}
		if r.URL.Path == "/error-page" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "<!doctype html><title>Fixture error</title><p>Requested page does not exist</p>")
			return
		}
		if r.URL.Path == "/redirect-error" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<!doctype html><script>location.replace('/error-page')</script>`)
			return
		}
		if r.URL.Path == "/login" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<!doctype html><title>Fixture login</title>
<button>看过</button>
<form action="/login-complete" method="post">
<label>Account <input id="login-account" name="account"></label>
<label>Password <input id="login-password" type="password" name="password"></label>
<button id="login-submit" type="submit">Sign in</button></form>`)
			return
		}
		if r.URL.Path == "/login-complete" {
			if r.Method != http.MethodPost || r.FormValue("account") != "fixture-user" ||
				r.FormValue("password") != "fixture-password" {
				http.Error(w, "Fixture login failed", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, "<!doctype html><title>Fixture signed in</title><p>Fixture login complete</p>")
			return
		}
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
<button id="popup" onclick="window.open('/popup', '_blank', 'width=480,height=320')">Open popup</button>
<p id="scroll-target" style="margin-top:2400px">Scroll destination</p>`,
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
		"pairing": link, "extension": extension, "fixture": fixture.URL,
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
	if _, err = call(ctx, scope, "chat", "scroll_to", map[string]any{"selector": "#scroll-target"}); err != nil {
		t.Fatal(err)
	}
	position, err := call(ctx, scope, "chat", "evaluate", map[string]any{"expression": "window.scrollY"})
	var scroll struct {
		Value float64 `json:"value"`
	}
	if err != nil || json.Unmarshal(position, &scroll) != nil || scroll.Value < 1000 {
		t.Fatalf("scroll_to did not move viewport: %s, %v", position, err)
	}
	if _, err = call(ctx, scope, "chat", "wheel", map[string]any{"delta_y": -600}); err != nil {
		t.Fatal(err)
	}
	startY := scroll.Value
	scrollDeadline := time.Now().Add(3 * time.Second)
	for {
		position, err = call(ctx, scope, "chat", "evaluate",
			map[string]any{"expression": "(() => { return window.scrollY; })()"})
		if err != nil || json.Unmarshal(position, &scroll) != nil {
			t.Fatalf("scroll position unavailable: %s, %v", position, err)
		}
		if scroll.Value < startY {
			break
		}
		if time.Now().After(scrollDeadline) {
			t.Fatal("wheel did not move viewport")
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, method := range []string{"scroll_to", "focus", "blur"} {
		if _, err = call(ctx, scope, "chat", method, map[string]any{"selector": "#name"}); err != nil {
			t.Fatal(err)
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
	hostAck := func(command, expected string) {
		t.Helper()
		if _, err := fmt.Fprintln(input, command); err != nil {
			t.Fatal(err)
		}
		select {
		case line := <-hostLines:
			if line != expected {
				t.Fatalf("browser host %s: %s", command, line)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	assertDenied := func(tabID int) {
		t.Helper()
		for _, method := range []string{"tab_select", "snapshot", "tab_close"} {
			_, err := call(ctx, scope, "chat", method, map[string]any{"tab_id": tabID})
			if err == nil || !strings.Contains(err.Error(), "permission_denied") {
				t.Fatalf("unauthorized tab %d accepted %s: %v", tabID, method, err)
			}
		}
	}
	borrowTab := func(tabID int) {
		t.Helper()
		done := make(chan error, 1)
		go func() {
			_, err := call(ctx, scope, "chat", "tab_borrow", map[string]any{"tab_id": tabID})
			done <- err
		}()
		for !m.Status(scope, "chat").NeedsHelp {
			select {
			case err := <-done:
				t.Fatalf("borrow finished without browser confirmation: %v", err)
			case <-tick.C:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
		if m.Status(scope, "chat").Action != "tab_borrow" {
			t.Fatal("borrow confirmation must identify its handoff separately from login help")
		}
		hostAck("approve-borrow", "approve-borrow-done")
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	returnTab := func(tabID int) {
		t.Helper()
		hostAck(fmt.Sprintf("before-tab-return %d", tabID), "return-recorded")
		result, err := call(ctx, scope, "chat", "tab_return", map[string]any{"tab_id": tabID})
		if err != nil {
			t.Fatal(err)
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, result); err != nil {
			t.Fatal(err)
		}
		hostAck("expect-returned-tab "+compact.String(), "returned-tab-preserved")
		assertDenied(tabID)
	}
	// Same-window navigation targets are observed; independent popup windows
	// require the official borrow/return flow, even when opened by native input.
	for _, target := range []struct {
		selector string
		path     string
		borrow   bool
	}{
		{"#new-tab", "/linked", false},
		{"#popup", "/popup", true},
	} {
		t.Logf("navigation target %s (borrow=%t)", target.selector, target.borrow)
		if _, err = call(ctx, scope, "chat", "click", map[string]any{"selector": target.selector}); err != nil {
			t.Fatal(err)
		}
		popupID := 0
		deadline := time.After(3 * time.Second)
		for popupID == 0 {
			listed, err := call(ctx, scope, "chat", "tab_list", map[string]any{"scope": "all"})
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				Tabs []struct {
					ID  int    `json:"tab_id"`
					URL string `json:"url"`
				} `json:"tabs"`
			}
			if err := json.Unmarshal(listed, &result); err != nil {
				t.Fatal(err)
			}
			for _, tab := range result.Tabs {
				if tab.URL == fixture.URL+target.path {
					popupID = tab.ID
				}
			}
			if popupID != 0 {
				break
			}
			select {
			case <-tick.C:
			case <-deadline:
				t.Fatalf("fixture popup missing: %s", listed)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
		if target.borrow {
			assertDenied(popupID)
			borrowTab(popupID)
		}
		if _, err = call(ctx, scope, "chat", "tab_select", map[string]any{"tab_id": popupID}); err != nil {
			t.Fatal(err)
		}
		if _, err = call(ctx, scope, "chat", "snapshot", map[string]any{"tab_id": popupID}); err != nil {
			t.Fatal(err)
		}
		if target.borrow {
			if _, err = call(ctx, scope, "chat", "tab_close", map[string]any{"tab_id": popupID}); err == nil ||
				!strings.Contains(err.Error(), "invalid_params") {
				t.Fatalf("borrowed popup was closable: %v", err)
			}
			returnTab(popupID)
			// Keep the returned page: later cleanup checks must prove that
			// task stop preserves it and releases its debugger attachment.
		} else if _, err = call(ctx, scope, "chat", "tab_close", map[string]any{"tab_id": popupID}); err != nil {
			t.Fatal(err)
		}
	}
	// A genuine user-created tab inside the task window remains unauthorized and
	// cannot be borrowed in place (upstream PR #297). Once moved to a regular
	// window it can be borrowed with an actual browser confirmation.
	_, _ = io.WriteString(input, "create-unowned-tab\n")
	var userTab struct {
		ID int `json:"tab_id"`
	}
	select {
	case line := <-hostLines:
		if err = json.Unmarshal([]byte(line), &userTab); err != nil || userTab.ID == 0 {
			t.Fatalf("create fixture tab: %s", line)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for _, method := range []string{"snapshot", "tab_select", "tab_close"} {
		if _, err = call(ctx, scope, "chat", method, map[string]any{"tab_id": userTab.ID}); err == nil ||
			!strings.Contains(err.Error(), "permission_denied") {
			t.Fatalf("unowned user tab accepted %s: %v", method, err)
		}
	}
	if _, err = call(ctx, scope, "chat", "tab_borrow", map[string]any{"tab_id": userTab.ID}); err == nil ||
		!strings.Contains(err.Error(), "invalid_params") {
		t.Fatalf("unowned tab inside the Agent Window was borrowed in place: %v", err)
	}
	_, _ = fmt.Fprintf(input, "move-fixture-tab-out %d\n", userTab.ID)
	select {
	case line := <-hostLines:
		if line != "fixture-tab-moved" {
			t.Fatalf("move fixture tab: %s", line)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	borrowTab(userTab.ID)
	if _, err = call(ctx, scope, "chat", "snapshot", map[string]any{"tab_id": userTab.ID}); err != nil {
		t.Fatal(err)
	}
	returnTab(userTab.ID)
	_, _ = fmt.Fprintf(input, "remove-fixture-tab %d\n", userTab.ID)
	select {
	case line := <-hostLines:
		if line != "fixture-tab-removed" {
			t.Fatalf("fixture cleanup: %s", line)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
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
	// A fully loaded HTTP error is still a completed navigation. Inline redirects
	// may replace the initial loader before its DOMContentLoaded event.
	for _, path := range []string{"/error-page", "/redirect-error"} {
		result, navErr := call(ctx, scope, "chat", "navigate", map[string]any{
			"url": fixture.URL + path, "timeout_ms": 2000,
		})
		if navErr != nil || !strings.Contains(string(result), `"reached":"domcontentloaded"`) {
			t.Fatalf("loaded error-page navigation %s: %s, %v", path, result, navErr)
		}
		observed, observeErr := call(ctx, scope, "chat", "snapshot", nil)
		if observeErr != nil || !strings.Contains(string(observed), "Requested page does not exist") {
			t.Fatalf("error page must remain readable: %s, %v", observed, observeErr)
		}
	}
	// Rendering an error message does not imply DOMContentLoaded. The extension
	// must report its own lifecycle timeout before the outer RPC deadline, leaving
	// observation available to inspect the displayed error instead of pausing.
	slowError, navErr := call(ctx, scope, "chat", "navigate", map[string]any{
		"url": fixture.URL + "/slow-error-page", "timeout_ms": 200,
	})
	if navErr != nil || !NavigationIncomplete("navigate", slowError) {
		t.Fatalf("expected inspectable navigation timeout, got %s, %v", slowError, navErr)
	}
	if m.Status(scope, "chat").Paused {
		t.Fatal("a reported navigation wait timeout must not block page observation")
	}
	errorPage, observeErr := call(ctx, scope, "chat", "snapshot", nil)
	if observeErr != nil || !strings.Contains(string(errorPage), "Visible error while response is loading") {
		t.Fatalf("cannot inspect error page after navigation timeout: %s, %v", errorPage, observeErr)
	}
	// The human can fill a login form and navigate; help reappears in the new
	// document and its completion releases the same pending RPC.
	if _, err = call(ctx, scope, "chat", "navigate", map[string]any{"url": fixture.URL + "/login"}); err != nil {
		t.Fatal(err)
	}
	go func() {
		result, e := call(ctx, scope, "chat", "request_help", map[string]any{
			"prompt": "Confirm this fixture step", "timeout_ms": 10000,
			// This text exists before login. Legacy criteria must not complete
			// the help RPC before the human confirms the actual login.
			"completion_criteria": map[string]any{"any": []map[string]string{{"text_exists": "看过"}}},
		})
		if e == nil && !strings.Contains(string(result), `"continued"`) {
			e = fmt.Errorf("unexpected help outcome: %s", result)
		}
		helpDone <- e
	}()
	for !m.Status(scope, "chat").NeedsHelp {
		select {
		case err := <-helpDone:
			t.Fatalf("help completed before handoff: %v", err)
		case <-tick.C:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	// Give the upstream automatic detector time to run. No human has acted.
	select {
	case err := <-helpDone:
		t.Fatalf("help completed without manual confirmation: %v", err)
	case <-time.After(2500 * time.Millisecond):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if !m.Status(scope, "chat").NeedsHelp {
		t.Fatal("help prompt disappeared before the user completed login")
	}
	_, _ = io.WriteString(input, "complete-login\n")
	select {
	case line := <-hostLines:
		if line != "complete-login-done" {
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
	loggedIn, err := call(ctx, scope, "chat", "snapshot", nil)
	if err != nil || !strings.Contains(string(loggedIn), "Fixture login complete") {
		t.Fatalf("agent could not resume after manual login: %s, %v", loggedIn, err)
	}
	if _, err = call(ctx, scope, "chat", "navigate", map[string]any{"url": fixture.URL}); err != nil {
		t.Fatal(err)
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
		t.Fatalf("turn completion lost retained task: %+v", status)
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
	_, _ = io.WriteString(input, "check-detached\n")
	select {
	case line := <-hostLines:
		if line != `{"detached":false}` {
			t.Fatalf("retained official session unexpectedly detached: %s", line)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err = m.Preview(ctx, scope, "chat"); err != nil {
		t.Fatal(err)
	}
	if _, err = call(ctx, scope, "chat", "snapshot", nil); err != nil {
		t.Fatal(err)
	}
	if m.Status(scope, "chat").Idle {
		t.Fatal("next operation did not resume preview polling")
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
				t.Fatalf("browser cleanup changed user tabs or leaked task tabs: %s", line)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	checkCleanup()
	// Closing the final page through the agent is normal cleanup, not a user
	// interrupt. The following turn must start without a manual Resume click.
	if err = m.Control(ctx, scope, "chat", "select"); err != nil {
		t.Fatal(err)
	}
	lastPage, err := call(ctx, scope, "chat", "navigate", map[string]any{"url": fixture.URL})
	var lastTab struct {
		ID int `json:"tab_id"`
	}
	if err != nil || json.Unmarshal(lastPage, &lastTab) != nil || lastTab.ID == 0 {
		t.Fatalf("last tab fixture: %s, %v", lastPage, err)
	}
	if _, err = call(ctx, scope, "chat", "tab_close", map[string]any{"tab_id": lastTab.ID}); err != nil {
		t.Fatal(err)
	}
	// The model streams its answer before turn cleanup. Let Chrome's queued
	// window-removal event arrive instead of racing it with session.stop.
	time.Sleep(250 * time.Millisecond)
	if m.Status(scope, "chat").Paused {
		t.Fatal("agent tab_close was reported as a user interrupt")
	}
	if err = m.FinishTurn(ctx, scope, "chat", false); err != nil {
		t.Fatal(err)
	}
	if m.Status(scope, "chat").Paused {
		t.Fatal("agent closing the final tab incorrectly paused the task")
	}
	checkCleanup()
	// Closing a window outside the agent command must still pause the task.
	if err = m.Control(ctx, scope, "chat", "select"); err != nil {
		t.Fatal(err)
	}
	lastPage, err = call(ctx, scope, "chat", "navigate", map[string]any{"url": fixture.URL})
	if err != nil || json.Unmarshal(lastPage, &lastTab) != nil || lastTab.ID == 0 {
		t.Fatalf("manual close fixture: %s, %v", lastPage, err)
	}
	_, _ = fmt.Fprintf(input, "close-fixture-window %d\n", lastTab.ID)
	select {
	case line := <-hostLines:
		if line != "fixture-window-closed" {
			t.Fatalf("manual window closure: %s", line)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	closeDeadline := time.After(5 * time.Second)
	for !m.Status(scope, "chat").Paused {
		select {
		case <-tick.C:
		case <-closeDeadline:
			t.Fatal("manual window closure did not pause task")
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if _, err = call(ctx, scope, "chat", "navigate", map[string]any{"url": fixture.URL}); err == nil ||
		!strings.Contains(err.Error(), "task_paused") {
		t.Fatalf("manual window closure did not block automation: %v", err)
	}
	if err = m.Control(ctx, scope, "chat", "resume"); err != nil {
		t.Fatal(err)
	}
	if err = m.FinishTurn(ctx, scope, "chat", false); err != nil {
		t.Fatal(err)
	}
	checkCleanup()
	// Repeated successful turns preserve the original and returned user tabs,
	// with no agent-created tabs left over. The next turn needs no Resume.
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
