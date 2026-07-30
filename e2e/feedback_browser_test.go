//go:build feedback_e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

type browserFeedbackState struct {
	mu       sync.Mutex
	rating   string
	reason   string
	likes    int
	dislikes int
	reset    bool
}

func TestIssue1248FeedbackBrowserE2E(t *testing.T) {
	state := &browserFeedbackState{likes: 2, dislikes: 1}
	backend := httptest.NewServer(feedbackBrowserHandler(t, state))
	defer backend.Close()

	repoRoot := feedbackBrowserRepoRoot(t)
	port := feedbackBrowserFreePort(t)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()

	frontendRoot := filepath.Join(repoRoot, "frontend")
	viteEntrypoint := filepath.Join(frontendRoot, "node_modules", "vite", "bin", "vite.js")
	command := exec.CommandContext(ctx, "node", viteEntrypoint, "--port", port, "--strictPort")
	command.Dir = frontendRoot
	command.Env = append(os.Environ(), "VITE_DEV_PROXY_TARGET="+backend.URL)
	var viteOutput bytes.Buffer
	command.Stdout = &viteOutput
	command.Stderr = &viteOutput
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = command.Process.Kill()
	}()

	pageURL := "http://127.0.0.1:" + port + "/e2e/feedback.html"
	feedbackBrowserWaitForServer(t, ctx, pageURL, &viteOutput)

	allocatorOptions := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	if executable := feedbackBrowserChromeExecutable(); executable != "" {
		allocatorOptions = append(allocatorOptions, chromedp.ExecPath(executable))
	}
	allocatorOptions = append(allocatorOptions,
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	allocatorContext, cancelAllocator := chromedp.NewExecAllocator(ctx, allocatorOptions...)
	defer cancelAllocator()
	browserContext, cancelBrowser := chromedp.NewContext(allocatorContext)
	defer cancelBrowser()
	var diagnosticsMu sync.Mutex
	var diagnostics strings.Builder
	chromedp.ListenTarget(browserContext, func(event any) {
		diagnosticsMu.Lock()
		defer diagnosticsMu.Unlock()
		switch value := event.(type) {
		case *cdpruntime.EventExceptionThrown:
			diagnostics.WriteString(value.ExceptionDetails.Text)
			if value.ExceptionDetails.Exception != nil {
				diagnostics.WriteString(": ")
				diagnostics.WriteString(value.ExceptionDetails.Exception.Description)
			}
			diagnostics.WriteByte('\n')
		case *cdpruntime.EventConsoleAPICalled:
			diagnostics.WriteString(string(value.Type))
			for _, argument := range value.Args {
				diagnostics.WriteByte(' ')
				if len(argument.Value) > 0 {
					diagnostics.Write(argument.Value)
				} else {
					diagnostics.WriteString(argument.Description)
				}
			}
			diagnostics.WriteByte('\n')
		}
	})

	runBrowserStep := func(label string, actions ...chromedp.Action) {
		t.Helper()
		if err := chromedp.Run(browserContext, actions...); err != nil {
			t.Fatalf("%s: %v\nvite output:\n%s", label, err, viteOutput.String())
		}
	}

	runBrowserStep("navigate to feedback harness",
		chromedp.EmulateViewport(1280, 900),
		chromedp.Navigate(pageURL),
		chromedp.Sleep(3*time.Second),
	)
	var initialBody string
	var initialHTML string
	runBrowserStep("inspect feedback harness",
		chromedp.Text(`body`, &initialBody, chromedp.ByQuery),
		chromedp.OuterHTML(`html`, &initialHTML, chromedp.ByQuery),
	)
	if !strings.Contains(initialBody, "Issue 1248 feedback E2E") {
		diagnosticsMu.Lock()
		browserDiagnostics := diagnostics.String()
		diagnosticsMu.Unlock()
		t.Fatalf("feedback harness did not mount; body = %q\nhtml:\n%s\nbrowser:\n%s\nvite output:\n%s",
			initialBody, boundedDiagnostic(initialHTML), browserDiagnostics, viteOutput.String())
	}
	runBrowserStep("load feedback harness",
		chromedp.WaitVisible(`#answer-feedback`, chromedp.ByQuery),
		chromedp.WaitVisible(`#governance`, chromedp.ByQuery),
	)
	runBrowserStep("load governance list",
		chromedp.Poll(`document.body.textContent.includes("Browser chunk")`, nil),
	)
	runBrowserStep("like answer",
		chromedp.Click(`button[title="Like"]`, chromedp.ByQuery),
		chromedp.Poll(`document.querySelector("#feedback-state")?.textContent === "like"`, nil),
	)
	runBrowserStep("clear answer",
		chromedp.Click(`button[title="Like"]`, chromedp.ByQuery),
		chromedp.Poll(`document.querySelector("#feedback-state")?.textContent === "none"`, nil),
	)
	runBrowserStep("like answer before switch",
		chromedp.Click(`button[title="Like"]`, chromedp.ByQuery),
		chromedp.Poll(`document.querySelector("#feedback-state")?.textContent === "like"`, nil),
	)
	runBrowserStep("open dislike reasons",
		chromedp.Click(`button[title="Dislike"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`//button[contains(., "Inaccurate")]`, chromedp.BySearch),
	)
	runBrowserStep("switch answer to dislike",
		chromedp.Evaluate(`
			[...document.querySelectorAll("button")]
				.find((button) => button.textContent.trim() === "Inaccurate")
				.click()
		`, nil),
		chromedp.Poll(`document.querySelector("#feedback-state")?.textContent === "dislike"`, nil),
	)
	runBrowserStep("open governance detail",
		chromedp.Click(`//button[contains(., "View")]`, chromedp.BySearch),
		chromedp.WaitVisible(`//pre[contains(., "Full browser chunk content")]`, chromedp.BySearch),
	)

	state.mu.Lock()
	if state.rating != "dislike" || state.reason != "inaccurate" ||
		state.likes != 2 || state.dislikes != 2 {
		t.Fatalf("switch state = rating %q reason %q likes %d dislikes %d",
			state.rating, state.reason, state.likes, state.dislikes)
	}
	state.mu.Unlock()

	var confirmationVisible bool
	runBrowserStep("open governed reset confirmation",
		chromedp.Evaluate(`document.querySelector(".detail-heading button").click()`, nil),
		chromedp.Sleep(time.Second),
		chromedp.Evaluate(`Boolean(document.querySelector(".t-popconfirm"))`, &confirmationVisible),
	)
	if !confirmationVisible {
		t.Fatal("reset confirmation popup did not open")
	}
	var confirmationClicked bool
	runBrowserStep("confirm governed reset",
		chromedp.Evaluate(`
			(() => {
				const popup = [...document.querySelectorAll(".t-popconfirm")]
					.find((node) => node.getBoundingClientRect().width > 0)
				const buttons = popup ? [...popup.querySelectorAll("button")] : []
				if (!buttons.length) return false
				buttons[buttons.length - 1].click()
				return true
			})()
		`, &confirmationClicked),
		chromedp.Sleep(time.Second),
		chromedp.EmulateViewport(390, 844),
	)
	if !confirmationClicked {
		t.Fatal("reset confirmation button was not rendered")
	}

	var fitsMobile bool
	if err := chromedp.Run(browserContext, chromedp.Evaluate(
		`document.querySelector("#answer-feedback").getBoundingClientRect().right <= 390`,
		&fitsMobile,
	)); err != nil {
		t.Fatal(err)
	}
	if !fitsMobile {
		t.Fatal("feedback controls overflow the 390px viewport")
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.reset || state.likes != 0 || state.dislikes != 0 {
		t.Fatalf("reset state = reset %v likes %d dislikes %d", state.reset, state.likes, state.dislikes)
	}
}

func boundedDiagnostic(value string) string {
	const limit = 4096
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "\n... diagnostic truncated ..."
}

func feedbackBrowserHandler(t *testing.T, state *browserFeedbackState) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/sessions/session-e2e/messages/message-e2e/feedback", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var request struct {
			Type       string `json:"type"`
			ReasonCode string `json:"reason_code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		state.mu.Lock()
		if state.rating == "like" {
			state.likes--
		} else if state.rating == "dislike" {
			state.dislikes--
		}
		state.rating = ""
		state.reason = ""
		if request.Type == "like" {
			state.rating = "like"
			state.likes++
		} else if request.Type == "dislike" {
			state.rating = "dislike"
			state.reason = request.ReasonCode
			state.dislikes++
		}
		response := any(nil)
		if state.rating != "" {
			response = map[string]any{"type": state.rating, "reason_code": state.reason}
		}
		state.mu.Unlock()
		feedbackBrowserJSON(w, map[string]any{"success": true, "data": response})
	})
	mux.HandleFunc("/api/v1/knowledge-bases/kb-e2e/chunk-feedback", func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		item := feedbackBrowserChunk(state, false)
		state.mu.Unlock()
		feedbackBrowserJSON(w, map[string]any{
			"success": true,
			"data":    map[string]any{"total": 1, "page": 1, "page_size": 20, "data": []any{item}},
		})
	})
	mux.HandleFunc("/api/v1/knowledge-bases/kb-e2e/chunk-feedback/chunk-e2e/history", func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		reset := state.reset
		state.mu.Unlock()
		history := []any{map[string]any{
			"id": 1, "action": "feedback_weight_changed", "trigger_source": "dislike",
			"old_weight": 1, "new_weight": 0.8, "created_at": "2026-07-30T00:00:00Z",
		}}
		if reset {
			history = append(history, map[string]any{
				"id": 2, "action": "feedback_reset", "trigger_source": "admin_reset",
				"old_weight": 0.8, "new_weight": 1, "created_at": "2026-07-30T00:01:00Z",
			})
		}
		feedbackBrowserJSON(w, map[string]any{
			"success": true,
			"data":    map[string]any{"total": len(history), "page": 1, "page_size": 20, "data": history},
		})
	})
	mux.HandleFunc("/api/v1/knowledge-bases/kb-e2e/chunk-feedback/chunk-e2e/reset", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		state.mu.Lock()
		state.likes = 0
		state.dislikes = 0
		state.reset = true
		detail := feedbackBrowserChunk(state, true)
		state.mu.Unlock()
		feedbackBrowserJSON(w, map[string]any{"success": true, "data": detail})
	})
	mux.HandleFunc("/api/v1/knowledge-bases/kb-e2e/chunk-feedback/chunk-e2e", func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		detail := feedbackBrowserChunk(state, true)
		state.mu.Unlock()
		feedbackBrowserJSON(w, map[string]any{"success": true, "data": detail})
	})
	return mux
}

func feedbackBrowserChunk(state *browserFeedbackState, detail bool) map[string]any {
	result := map[string]any{
		"chunk_id": "chunk-e2e", "knowledge_id": "knowledge-e2e", "knowledge_base_id": "kb-e2e",
		"knowledge_title": "Browser document", "chunk_index": 1, "chunk_type": "text",
		"content_preview": "Browser chunk", "like_count": state.likes, "dislike_count": state.dislikes,
		"session_count": 1, "positive_rate": 0.5, "stored_recall_weight": 0.8,
		"effective_recall_weight": 0.8, "needs_optimization": true,
		"updated_at": "2026-07-30T00:00:00Z",
	}
	if detail {
		result["content"] = "Full browser chunk content"
		result["reason_counts"] = map[string]int{"inaccurate": state.dislikes}
		result["audits"] = []any{}
	}
	return result
}

func feedbackBrowserJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func feedbackBrowserRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve E2E test path")
	}
	return filepath.Dir(filepath.Dir(file))
}

func feedbackBrowserFreePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)
}

func feedbackBrowserWaitForServer(t *testing.T, ctx context.Context, target string, output *bytes.Buffer) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err == nil {
			if response, requestErr := client.Do(request); requestErr == nil {
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK {
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("Vite did not become ready: %v\n%s", ctx.Err(), output.String())
		case <-ticker.C:
		}
	}
}

func feedbackBrowserChromeExecutable() string {
	if configured := os.Getenv("CHROME_BIN"); configured != "" {
		return configured
	}
	if runtime.GOOS != "windows" {
		return ""
	}
	candidates := []string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func TestFeedbackBrowserHarnessHasNoExternalOrigins(t *testing.T) {
	root := feedbackBrowserRepoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "frontend", "e2e", "feedback.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "http://") || strings.Contains(string(body), "https://") {
		t.Fatal("browser harness must use only the isolated same-origin API")
	}
}
