package skillrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL, credential string
	httpClient          *http.Client
}

func NewClient(baseURL, credential string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 65 * time.Second
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), credential: credential, httpClient: &http.Client{Timeout: timeout}}
}

func (client *Client) Execute(ctx context.Context, request ExecuteRequest) (ExecuteResponse, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return ExecuteResponse{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/v1/execute", bytes.NewReader(body))
	if err != nil {
		return ExecuteResponse{}, err
	}
	httpRequest.Header.Set("Authorization", "Bearer "+client.credential)
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(httpRequest)
	if err != nil {
		return ExecuteResponse{}, fmt.Errorf("%w: %v", ErrRunnerUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ExecuteResponse{}, fmt.Errorf("runner status %d", response.StatusCode)
	}
	var result ExecuteResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return ExecuteResponse{}, err
	}
	return result, nil
}

func (client *Client) Healthy(ctx context.Context) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+"/health", nil)
	if err != nil {
		return false
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode == http.StatusOK
}
