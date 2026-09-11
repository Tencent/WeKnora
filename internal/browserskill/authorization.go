package browserskill

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func randomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func validToken(token string) bool {
	b, e := base64.RawURLEncoding.DecodeString(token)
	return e == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == token
}

// AuthorizeHTTP is extension-only, independent of browser cookies. Device
// tokens stay in chrome.storage.local; only hashes are committed to the DB.
func (m *Manager) AuthorizeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !m.Enabled() || m.store == nil {
		http.Error(w, "browser unavailable", http.StatusServiceUnavailable)
		return
	}
	if !validExtensionOrigin(r.Header.Get("Origin")) {
		http.Error(w, "extension origin required", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	var input struct {
		Action    string `json:"action"`
		NextToken string `json:"next_token"`
		Label     string `json:"label"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if !validToken(token) || json.NewDecoder(r.Body).Decode(&input) != nil || !validToken(input.NextToken) ||
		token == input.NextToken {
		http.Error(w, "invalid authorization request", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var record *DeviceRecord
	var err error
	switch input.Action {
	case "pair":
		label := strings.TrimSpace(input.Label)
		if label == "" {
			label = "Chrome"
		}
		if len([]rune(label)) > 100 {
			http.Error(w, "device label too long", http.StatusBadRequest)
			return
		}
		now := time.Now()
		record, err = m.store.exchange(
			ctx,
			tokenHash(token),
			DeviceRecord{
				ID:         randomID(),
				Label:      label,
				TokenHash:  tokenHash(input.NextToken),
				ExpiresAt:  now.Add(deviceLifetime),
				RenewAfter: now.Add(renewalInterval),
				CreatedAt:  now,
				LastSeenAt: now,
			},
		)
		if err == nil {
			m.disconnectScope(record.scope())
		}
	case "renew":
		record, err = m.store.renew(ctx, tokenHash(token), tokenHash(input.NextToken))
	default:
		http.Error(w, "invalid action", http.StatusBadRequest)
		return
	}
	if err != nil {
		if errors.Is(err, ErrAuthorization) {
			http.Error(w, "authorization expired or already used", http.StatusUnauthorized)
		} else {
			http.Error(w, "authorization storage unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).
		Encode(map[string]any{
			"device_id": record.ID, "service_name": "WeKnora",
			"expires_at": record.ExpiresAt, "renew_after": record.RenewAfter,
		})
}

func (m *Manager) watchLease(
	ctx context.Context,
	d *device,
	id, key string,
	conn, up *websocket.Conn,
	generation uint64,
) {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			check, cancel := context.WithTimeout(ctx, 5*time.Second)
			record, err := m.store.heartbeat(check, id, key)
			cancel()
			if err != nil {
				_ = conn.Close()
				_ = up.Close()
				return
			}
			d.mu.Lock()
			if d.generation == generation {
				d.expires = record.ExpiresAt
			}
			d.mu.Unlock()
		}
	}
}

// AccountStatus combines durable device metadata with live connection status.
type AccountStatus struct {
	ExtensionAvailable bool `json:"extension_available"`
	Status
	Device *DeviceRecord `json:"device,omitempty"`
}

// Account reads the member's device authorization independently of a conversation.
func (m *Manager) Account(ctx context.Context, s Scope) (AccountStatus, error) {
	result := AccountStatus{Status: Status{Enabled: m.Enabled()}, ExtensionAvailable: extensionPath() != ""}
	if m == nil || m.store == nil || !m.Enabled() {
		return result, nil
	}
	r, err := m.store.account(ctx, s)
	if err != nil {
		return result, err
	}
	if r != nil && r.RevokedAt == nil && time.Now().Before(r.ExpiresAt) {
		result.Device = r
		status, e := m.GetStatus(ctx, s, "")
		if e != nil {
			return result, e
		}
		result.Connected = status.Connected
	}
	return result, nil
}

// GetStatus resolves task status from the connection owner and interruption store.
func (m *Manager) GetStatus(ctx context.Context, s Scope, session string) (Status, error) {
	if result, remote, err := m.route(ctx, s, session, "status", "", nil); remote || err != nil {
		var status Status
		if err == nil {
			err = json.Unmarshal(result, &status)
		}
		return status, err
	}
	status := m.Status(s, session)
	if status.Connected && m.store != nil {
		record, err := m.store.account(ctx, s)
		if err != nil {
			return status, err
		}
		if record == nil || record.RevokedAt != nil || time.Now().After(record.ExpiresAt) ||
			time.Now().After(record.LeaseUntil) ||
			record.Owner != m.nodeID {
			status.Connected = false
			status.SessionID = ""
			status.Paused = status.Selected
		}
	}
	if !status.Selected && m != nil && m.store != nil && session != "" {
		rows, err := m.store.tasks(ctx, s)
		if err != nil {
			return status, err
		}
		for _, row := range rows {
			if row.Session == session {
				status.Selected = true
				status.Paused = true
				break
			}
		}
	}
	return status, nil
}

func extensionPath() string {
	p := os.Getenv("BROWSERSKILL_EXTENSION_PATH")
	if p == "" {
		return ""
	}
	info, err := os.Stat(p)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return p
}

// DownloadExtension serves the configured extension archive to an authenticated user.
func (m *Manager) DownloadExtension(w http.ResponseWriter, r *http.Request) {
	p := extensionPath()
	if !m.Enabled() || p == "" {
		http.Error(w, "extension package is not configured", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="browser-skill-weknora.zip"`)
	http.ServeFile(w, r, p)
}
