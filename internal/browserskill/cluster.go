package browserskill

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const internalPath = "/api/v1/local-browser/internal"

type (
	localRPCKey    struct{}
	clusterRequest struct {
		Node      string         `json:"node"`
		Scope     Scope          `json:"scope"`
		Session   string         `json:"session"`
		Operation string         `json:"operation"`
		Method    string         `json:"method"`
		Params    map[string]any `json:"params,omitempty"`
	}
)

type clusterResponse struct {
	Data     json.RawMessage `json:"data,omitempty"`
	Error    string          `json:"error,omitempty"`
	RPCError *RPCError       `json:"rpc_error,omitempty"`
}

func signRPC(secret, timestamp string, body []byte) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(timestamp + "\n"))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

func validInternalURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil &&
		u.RawQuery == "" &&
		u.Fragment == "" &&
		(u.Path == "" || u.Path == "/")
}

// ValidateConfiguration checks storage, transport and replica routing requirements.
func (m *Manager) ValidateConfiguration() error {
	if !m.Enabled() {
		return nil
	}
	if m.store == nil {
		return errors.New("BrowserSkill requires persistent authorization storage")
	}
	if m.publicURL != "" {
		if _, err := pairingEndpoint(m.publicURL); err != nil {
			return err
		}
	}
	if m.internalURL != "" && (!validInternalURL(m.internalURL) || len(m.clusterSecret) < 32) {
		return errors.New(
			"BrowserSkill multi-replica mode requires a valid internal URL and a shared cluster secret of " +
				"at least 32 characters",
		)
	}
	return nil
}

func (m *Manager) route(
	ctx context.Context,
	s Scope,
	session, operation, method string,
	params map[string]any,
) (json.RawMessage, bool, error) {
	if m == nil || m.store == nil {
		return nil, false, nil
	}

	if err := m.store.member(ctx, s); err != nil {
		return nil, false, err
	}
	r, err := m.store.account(ctx, s)
	if err != nil {
		return nil, false, err
	}
	if r == nil || r.RevokedAt != nil || time.Now().After(r.ExpiresAt) {
		if operation == "call" || (operation == "preview" || operation == "focus") ||
			(operation == "control" && method != "stop" && method != "select") {
			return nil, false, ErrAuthorization
		}
		return nil, false, nil
	}
	if r.Owner == "" || time.Now().After(r.LeaseUntil) {
		if operation == "call" || operation == "preview" || operation == "focus" ||
			(operation == "control" && (method == "start" || method == "resume")) {
			return nil, false, errors.New("browser disconnected; wait for reconnection and resume the task")
		}
		return nil, false, nil
	}
	if r.Owner == m.nodeID {
		return nil, false, nil
	}
	if ctx.Value(localRPCKey{}) != nil {
		return nil, false, errors.New("browser owner changed; refresh status")
	}
	if len(m.clusterSecret) < 32 || !validInternalURL(r.OwnerURL) {
		return nil, false, errors.New("browser is connected to another node; configure BrowserSkill cluster routing")
	}
	req := clusterRequest{m.nodeID, s, session, operation, method, params}
	req.Node = r.Owner
	body, err := json.Marshal(req)
	if err != nil {
		return nil, false, err
	}
	forwardCtx, cancel := context.WithTimeout(ctx, clusterTimeout(operation, method))
	defer cancel()
	request, err := http.NewRequestWithContext(
		forwardCtx,
		http.MethodPost,
		strings.TrimRight(r.OwnerURL, "/")+internalPath,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, false, err
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := randomID()
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Browser-Timestamp", timestamp)
	request.Header.Set("X-Browser-Nonce", nonce)
	request.Header.Set("X-Browser-Signature", signRPC(m.clusterSecret, timestamp+"\n"+nonce, body))
	response, err := m.forwardClient.Do(request)
	if err != nil {
		return nil, true, errors.New("browser owner unavailable; do not replay interrupted operations")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 200 {
		return nil, true, errors.New("browser owner rejected the request; refresh connection status")
	}
	var result clusterResponse
	if json.NewDecoder(io.LimitReader(response.Body, maxFrame)).Decode(&result) != nil {
		return nil, true, errors.New("invalid browser owner response")
	}
	if result.RPCError != nil {
		result.RPCError.BoundDetails()
		return nil, true, result.RPCError
	}
	if result.Error != "" {
		return nil, true, errors.New(result.Error)
	}
	return result.Data, true, nil
}

// InternalHTTP accepts authenticated, replay-protected requests for this node's connections.
func (m *Manager) InternalHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost || len(m.clusterSecret) < 32 {
		http.Error(w, "unavailable", http.StatusNotFound)
		return
	}
	timestamp := r.Header.Get("X-Browser-Timestamp")
	nonce := r.Header.Get("X-Browser-Nonce")
	unix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || time.Since(time.Unix(unix, 0)) > time.Minute || time.Until(time.Unix(unix, 0)) > time.Minute {
		http.Error(w, "expired request", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	sig, err := hex.DecodeString(r.Header.Get("X-Browser-Signature"))
	want, _ := hex.DecodeString(signRPC(m.clusterSecret, timestamp+"\n"+nonce, body))
	if err != nil || !hmac.Equal(sig, want) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	if len(nonce) != 32 {
		http.Error(w, "invalid nonce", http.StatusUnauthorized)
		return
	}
	m.mu.Lock()
	if m.seenRPC == nil {
		m.seenRPC = map[string]time.Time{}
	}
	for key, expiry := range m.seenRPC {
		if time.Now().After(expiry) {
			delete(m.seenRPC, key)
		}
	}
	_, replayed := m.seenRPC[nonce]
	full := len(m.seenRPC) >= 8192
	if !replayed && !full {
		m.seenRPC[nonce] = time.Now().Add(2 * time.Minute)
	}
	m.mu.Unlock()
	if replayed || full {
		http.Error(w, "replayed or throttled request", http.StatusConflict)
		return
	}
	var input clusterRequest
	if json.Unmarshal(body, &input) != nil || input.Node != m.nodeID || !input.Scope.valid() {
		http.Error(w, "invalid owner request", http.StatusConflict)
		return
	}
	// Signed internal RPC is executed locally, never forwarded a second time.
	ctx, cancel := context.WithTimeout(
		context.WithValue(r.Context(), localRPCKey{}, true), clusterTimeout(input.Operation, input.Method),
	)
	defer cancel()
	result := clusterResponse{}
	switch input.Operation {
	case "status":
		var status Status
		status, err = m.GetStatus(ctx, input.Scope, input.Session)
		if err == nil {
			result.Data, _ = json.Marshal(status)
		}
	case "call":
		result.Data, err = m.Call(ctx, input.Scope, input.Session, input.Method, input.Params)
	case "control":
		err = m.Control(ctx, input.Scope, input.Session, input.Method)
		if err == nil {
			result.Data = json.RawMessage(`{}`)
		}
	case "preview":
		result.Data, err = m.Preview(ctx, input.Scope, input.Session)
	case "idle":
		err = m.Idle(ctx, input.Scope, input.Session)
	case "focus":
		err = m.Focus(ctx, input.Scope, input.Session)
	case "forget":
		m.forgetLocal(input.Scope, []string{input.Session})
	default:
		err = errors.New("unsupported browser operation")
	}
	if err != nil {
		result.Error = err.Error()
		if errors.As(err, &result.RPCError) {
			result.RPCError.BoundDetails()
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
