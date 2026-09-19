package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"time"
)

// RemoteRuntimeAgentController implements PluginRuntimeController by calling a
// remote runtime-agent over a Unix socket. Docker privileges stay in the agent;
// the app only holds the agent socket connection and rebases the returned
// control socket onto its own mount point of the shared runtime volume.
type RemoteRuntimeAgentController struct {
	socketPath       string
	authToken        string
	agentRuntimeRoot string
	localRuntimeRoot string
	client           *http.Client
}

func NewRemoteRuntimeAgentController(socketPath, authToken, agentRuntimeRoot, localRuntimeRoot string) *RemoteRuntimeAgentController {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "unix", socketPath)
		},
	}
	return &RemoteRuntimeAgentController{
		socketPath:       socketPath,
		authToken:        authToken,
		agentRuntimeRoot: agentRuntimeRoot,
		localRuntimeRoot: localRuntimeRoot,
		client:           &http.Client{Transport: transport, Timeout: 60 * time.Second},
	}
}

func (c *RemoteRuntimeAgentController) Start(ctx context.Context, req StartPluginRequest) (*PluginHandle, error) {
	respBody, err := c.post(ctx, "/v1/plugins/start", req)
	if err != nil {
		return nil, err
	}
	var handle PluginHandle
	if err := json.Unmarshal(respBody, &handle); err != nil {
		return nil, fmt.Errorf("decode runtime agent start response: %w", err)
	}
	handle.ControlSocket = c.mapControlSocket(handle.ControlSocket)
	return &handle, nil
}

func (c *RemoteRuntimeAgentController) Stop(ctx context.Context, pluginID string) error {
	_, err := c.post(ctx, "/v1/plugins/stop", map[string]string{"plugin_id": pluginID})
	return err
}

func (c *RemoteRuntimeAgentController) post(ctx context.Context, path string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://agent"+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call runtime agent: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("runtime agent %s failed (%d): %s", path, resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// mapControlSocket rebases the agent-side control socket path onto the app's
// own mount point of the shared runtime volume, so the app can dial it. The
// rebase is strict: a path not inside the agent root is returned unchanged.
func (c *RemoteRuntimeAgentController) mapControlSocket(agentSocket string) string {
	if c.agentRuntimeRoot == "" || c.localRuntimeRoot == "" {
		return agentSocket
	}
	rel, ok := rebasePath(c.agentRuntimeRoot, agentSocket)
	if !ok {
		return agentSocket
	}
	return filepath.Join(c.localRuntimeRoot, rel)
}
