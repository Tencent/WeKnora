package langfuse

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// client is a thin HTTP client for the Langfuse ingestion API.
type client struct {
	host       string
	auth       string // pre-computed "Basic <base64>"
	httpClient *http.Client
	debug      bool
}

func newClient(cfg Config) *client {
	credentials := cfg.PublicKey + ":" + cfg.SecretKey
	return &client{
		host: strings.TrimRight(cfg.Host, "/"),
		auth: "Basic " + base64.StdEncoding.EncodeToString([]byte(credentials)),
		httpClient: &http.Client{
			Timeout: cfg.RequestTimeout,
		},
		debug: cfg.Debug,
	}
}

type ingestionRequest struct {
	Batch []ingestionEvent `json:"batch"`
}

// ingest posts a batch of events to Langfuse. The API accepts partial failure:
// individual event errors are returned in a 207 response, which we surface as
// a logged debug message rather than a hard error (the batch as a whole was
// accepted).
func (c *client) ingest(ctx context.Context, events []ingestionEvent) error {
	if len(events) == 0 {
		return nil
	}

	body, err := json.Marshal(ingestionRequest{Batch: events})
	if err != nil {
		return fmt.Errorf("langfuse: marshal batch: %w", err)
	}

	endpoint := c.host + "/api/public/ingestion"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("langfuse: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("User-Agent", "weknora-langfuse/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("langfuse: ingest request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	// 207 = "multi-status": batch accepted, some events may have failed.
	// 2xx = success. Anything else is a transport/auth error worth surfacing.
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if c.debug && resp.StatusCode == http.StatusMultiStatus {
			return fmt.Errorf("langfuse: partial ingest success (207): %s", truncate(string(respBody), 512))
		}
		return nil
	}
	return fmt.Errorf("langfuse: ingest failed with status %d: %s", resp.StatusCode, truncate(string(respBody), 512))
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
