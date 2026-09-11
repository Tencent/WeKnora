package browserskill

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type uiReply struct {
	data json.RawMessage
	err  error
}

// Focus is user initiated. Only the server-owned task ID is sent to Chrome.
func (m *Manager) Focus(ctx context.Context, s Scope, session string) error {
	if _, remote, err := m.route(ctx, s, session, "focus", "", nil); remote || err != nil {
		return err
	}
	data, err := m.callUI(ctx, s, session, "gateway.task_focus")
	if err != nil {
		return err
	}
	var result struct {
		Focused bool `json:"focused"`
	}
	if json.Unmarshal(data, &result) != nil || !result.Focused {
		return errors.New("browser task tab unavailable")
	}
	return nil
}

// UI traffic bypasses the daemon's automation queue. This allows previews while
// a command waits for navigation or human input, without starting/resuming tasks.
func (m *Manager) callUI(ctx context.Context, s Scope, session, method string) (json.RawMessage, error) {
	d := m.get(s)
	if d == nil {
		return nil, errors.New("browser disconnected")
	}
	d.mu.Lock()
	t := d.tasks[session]
	if t == nil || t.id == "" || !d.ready || d.conn == nil {
		d.mu.Unlock()
		return nil, errors.New("browser task unavailable")
	}
	if len(d.uiCalls) >= 8 {
		d.mu.Unlock()
		return nil, errors.New("browser preview is busy")
	}
	id := "wk-ui-" + randomID()
	reply := make(chan uiReply, 1)
	if d.uiCalls == nil {
		d.uiCalls = map[string]chan uiReply{}
	}
	d.uiCalls[id] = reply
	conn, sid := d.conn, t.id
	d.mu.Unlock()
	defer func() { d.mu.Lock(); delete(d.uiCalls, id); d.mu.Unlock() }()
	d.writeMu.Lock()
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	err := conn.WriteJSON(map[string]any{"id": id, "method": method, "params": map[string]string{"session_id": sid}})
	d.writeMu.Unlock()
	if err != nil {
		return nil, errors.New("browser UI request failed")
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case result := <-reply:
		return result.data, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, errors.New("browser preview timed out; check the BrowserSkill extension")
	}
}

func (m *Manager) receiveUI(d *device, data []byte) bool {
	var frame struct {
		ID     string          `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(data, &frame) != nil || frame.ID == "" {
		return false
	}
	d.mu.Lock()
	reply := d.uiCalls[frame.ID]
	if reply != nil {
		delete(d.uiCalls, frame.ID)
	}
	d.mu.Unlock()
	if reply == nil {
		return len(frame.ID) > 6 && frame.ID[:6] == "wk-ui-"
	} // Late UI replies never reach native RPC.
	var err error
	if len(frame.Error) > 0 && string(frame.Error) != "null" {
		err = errors.New("browser task tab unavailable")
	}
	reply <- uiReply{frame.Result, err}
	return true
}
