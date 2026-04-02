package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"
	"time"

)

const (
	maxErrorBodyBytes = 4096 // cap on error response body reads
	execdPort         = 44772
)

var validSandboxIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

type openSandboxClient struct {
	apiURL     string
	apiKey     string
	httpClient *http.Client
}

// openSandboxCreateResponse is the response from POST /v1/sandboxes.
type openSandboxCreateResponse struct {
	ID string `json:"id"`
}

// openSandboxExecResponse aggregates stdout/stderr/exitCode from the execd streaming response.
type openSandboxExecResponse struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// openSandboxEndpointResponse is the response from GET /v1/sandboxes/{id}/endpoints/{port}.
type openSandboxEndpointResponse struct {
	Endpoint string `json:"endpoint"`
}

func newOpenSandboxClient(apiURL, apiKey string) *openSandboxClient {
	if !strings.HasPrefix(apiURL, "https://") && !strings.HasPrefix(apiURL, "http://") {
		apiURL = "https://" + apiURL
	}
	return &openSandboxClient{
		apiURL:     apiURL,
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// readErrorBody reads up to maxErrorBodyBytes from a response body for error messages.
func readErrorBody(body io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(body, maxErrorBodyBytes))
	return string(data)
}

// createSandbox creates a new sandbox with the given image.
// POST {apiURL}/v1/sandboxes
func (c *openSandboxClient) createSandbox(ctx context.Context, image string, timeout time.Duration) (*openSandboxCreateResponse, error) {
	reqBody := map[string]any{
		"image":          map[string]string{"uri": image},
		"resourceLimits": map[string]string{"cpu": "500m", "memory": "256Mi"},
		"entrypoint":     []string{"sleep", fmt.Sprintf("%d", int(timeout.Seconds())+60)},
	}
	if timeout > 0 {
		reqBody["timeout"] = int(timeout.Seconds()) + 60 // sandbox lives slightly longer than execution timeout
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("opensandbox: marshal create request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL+"/v1/sandboxes", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("opensandbox: build create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("OPEN-SANDBOX-API-KEY", c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opensandbox: create sandbox: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("opensandbox: create sandbox: unexpected status %d: %s", resp.StatusCode, readErrorBody(resp.Body))
	}

	var result openSandboxCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("opensandbox: decode create response: %w", err)
	}
	if !validSandboxIDPattern.MatchString(result.ID) {
		return nil, fmt.Errorf("opensandbox: invalid sandbox ID returned by API: %q", result.ID)
	}
	return &result, nil
}

// getExecdURL retrieves the execd endpoint for a sandbox and returns a full HTTP URL.
// GET {apiURL}/v1/sandboxes/{id}/endpoints/{port}
func (c *openSandboxClient) getExecdURL(ctx context.Context, sandboxID string) (string, error) {
	url := fmt.Sprintf("%s/v1/sandboxes/%s/endpoints/%d", c.apiURL, sandboxID, execdPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("opensandbox: build endpoint request: %w", err)
	}
	if c.apiKey != "" {
		req.Header.Set("OPEN-SANDBOX-API-KEY", c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("opensandbox: get endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("opensandbox: get endpoint: unexpected status %d: %s", resp.StatusCode, readErrorBody(resp.Body))
	}

	var result openSandboxEndpointResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("opensandbox: decode endpoint response: %w", err)
	}

	ep := result.Endpoint
	if !strings.HasPrefix(ep, "http://") && !strings.HasPrefix(ep, "https://") {
		ep = "http://" + ep
	}
	return ep, nil
}

// waitForExecd polls the execd /ping endpoint until it responds 200 or context expires.
func (c *openSandboxClient) waitForExecd(ctx context.Context, execdURL string) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("opensandbox: execd not ready: %w", ctx.Err())
		case <-ticker.C:
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, execdURL+"/ping", nil)
		if err != nil {
			continue
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return nil
		}
	}
}

// uploadFile uploads a file to the sandbox via multipart form.
// POST {execdURL}/files/upload with "metadata" file (JSON {"path": "..."}) + "file" file.
func (c *openSandboxClient) uploadFile(ctx context.Context, execdURL, filename, content string) error {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// metadata must be uploaded as a file part, not a text field
	metaPart, err := writer.CreateFormFile("metadata", "metadata.json")
	if err != nil {
		return fmt.Errorf("opensandbox: create metadata part: %w", err)
	}
	metaJSON, _ := json.Marshal(map[string]string{"path": "/workspace/" + filename})
	if _, err := metaPart.Write(metaJSON); err != nil {
		return fmt.Errorf("opensandbox: write metadata: %w", err)
	}

	filePart, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return fmt.Errorf("opensandbox: create form file: %w", err)
	}
	if _, err := io.WriteString(filePart, content); err != nil {
		return fmt.Errorf("opensandbox: write file content: %w", err)
	}
	writer.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, execdURL+"/files/upload", &buf)
	if err != nil {
		return fmt.Errorf("opensandbox: build upload request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("opensandbox: upload file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("opensandbox: upload file: unexpected status %d: %s", resp.StatusCode, readErrorBody(resp.Body))
	}
	return nil
}

// executeCommand runs a command inside the sandbox.
// POST {execdURL}/command with body {"command": command}.
// The response is streamed as JSONL events; this method collects stdout/stderr and exit code.
func (c *openSandboxClient) executeCommand(ctx context.Context, execdURL, command string) (*openSandboxExecResponse, error) {
	body, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		return nil, fmt.Errorf("opensandbox: marshal exec request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, execdURL+"/command", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("opensandbox: build exec request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opensandbox: execute command: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("opensandbox: execute command: unexpected status %d: %s", resp.StatusCode, readErrorBody(resp.Body))
	}

	return parseExecdStream(resp.Body)
}

// execdEvent represents a single event in the execd streaming response.
type execdEvent struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Error *struct {
		EValue string `json:"evalue"`
	} `json:"error,omitempty"`
}

// parseExecdStream reads the JSONL streaming response from execd and aggregates results.
func parseExecdStream(r io.Reader) (*openSandboxExecResponse, error) {
	var stdout, stderr strings.Builder
	exitCode := 0

	scanner := bufio.NewScanner(io.LimitReader(r, maxResponseBytes))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var event execdEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue // skip unparseable lines
		}

		switch event.Type {
		case "stdout":
			stdout.WriteString(event.Text)
		case "stderr":
			stderr.WriteString(event.Text)
		case "error":
			if event.Error != nil {
				// evalue contains the exit code as string
				fmt.Sscanf(event.Error.EValue, "%d", &exitCode)
			}
			if exitCode == 0 {
				exitCode = 1 // error event with no parseable exit code
			}
		}
	}

	return &openSandboxExecResponse{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}, nil
}

// deleteSandbox deletes a sandbox by ID.
// DELETE {apiURL}/v1/sandboxes/{id}
func (c *openSandboxClient) deleteSandbox(ctx context.Context, sandboxID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, fmt.Sprintf("%s/v1/sandboxes/%s", c.apiURL, sandboxID), nil)
	if err != nil {
		return fmt.Errorf("opensandbox: build delete request: %w", err)
	}
	if c.apiKey != "" {
		req.Header.Set("OPEN-SANDBOX-API-KEY", c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("opensandbox: delete sandbox: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("opensandbox: delete sandbox: unexpected status %d: %s", resp.StatusCode, readErrorBody(resp.Body))
	}
	return nil
}

// healthCheck returns true if the OpenSandbox lifecycle API is reachable and healthy.
// GET {apiURL}/health
func (c *openSandboxClient) healthCheck(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL+"/health", nil)
	if err != nil {
		return false
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}
