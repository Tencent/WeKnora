package host

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// GatewayPrefix starts the paths a plugin host serves its plugins under:
// /p/{pluginId}/{version}/{protocol path}.
const GatewayPrefix = "/p/"

// maxGatewayBody bounds one relayed request: a parse call carries the whole
// document.
const maxGatewayBody = 512 << 20

// Gateway serves the plugins this host runs to the other WeKnora nodes, at
// /p/{pluginId}/{version}/... Requests must be signed with the cluster key
// (pluginapi.Sign over the body); answers, streams included, are relayed as
// the plugin gives them.
func (m *Manager) Gateway(key []byte) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc(GatewayPrefix, func(w http.ResponseWriter, r *http.Request) {
		m.inFlight.Add(1)
		defer m.inFlight.Add(-1)
		m.relay(w, r, key)
	})
	return mux
}

func gatewayError(w http.ResponseWriter, e *pluginapi.Error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Code.HTTPStatus())
	_ = json.NewEncoder(w).Encode(pluginapi.ErrorBody{Error: *e})
}

func (m *Manager) relay(w http.ResponseWriter, r *http.Request, key []byte) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxGatewayBody+1))
	if err != nil {
		gatewayError(w, pluginapi.Errorf(pluginapi.CodeBadRequest, "read request: %v", err))
		return
	}
	if len(body) > maxGatewayBody {
		gatewayError(w, pluginapi.Errorf(pluginapi.CodeBadRequest, "request is over %d bytes", maxGatewayBody))
		return
	}
	err = pluginapi.VerifySignature(key, r.Header.Get(pluginapi.TimestampHeader),
		r.Header.Get(pluginapi.SignatureHeader), body, time.Now())
	if err != nil {
		gatewayError(w, pluginapi.Errorf(pluginapi.CodeUnauthorized, "gateway: %v", err))
		return
	}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, GatewayPrefix), "/", 3)
	if len(parts) < 3 || parts[0] == "" || parts[1] == "" {
		gatewayError(w, pluginapi.Errorf(pluginapi.CodeNotFound, "no endpoint %s", r.URL.Path))
		return
	}
	id, version, path := parts[0], parts[1], "/"+parts[2]
	m.mu.Lock()
	p := m.procs[id]
	if p == nil || p.spec.m.Version != version {
		// A caller not yet switched to an upgrade reaches the version it
		// knows while it is handed over.
		p = m.handingOver[id]
	}
	m.mu.Unlock()
	if p == nil || p.spec.m.Version != version {
		// The caller's view of this host is a heartbeat old.
		gatewayError(w, pluginapi.Errorf(pluginapi.CodeUnavailable, "this host does not run %s@%s", id, version))
		return
	}
	c, err := p.Client()
	if err != nil {
		if pe, ok := pluginapi.AsError(err); ok {
			gatewayError(w, pe)
		} else {
			gatewayError(w, pluginapi.Errorf(pluginapi.CodeUnavailable, "%v", err))
		}
		return
	}
	var sent []byte
	if len(body) > 0 {
		sent = body
	}
	resp, err := c.Raw(r.Context(), r.Method, path, sent, r.Header.Get(pluginapi.RequestIDHeader))
	if err != nil {
		gatewayError(w, pluginapi.Errorf(pluginapi.CodeUnavailable, "%v", err))
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			// Stream events go out as the plugin writes them.
			if flusher != nil && bytes.IndexByte(buf[:n], '\n') >= 0 {
				flusher.Flush()
			}
		}
		if err != nil {
			if flusher != nil {
				flusher.Flush()
			}
			return
		}
	}
}
