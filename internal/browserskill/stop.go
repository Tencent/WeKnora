package browserskill

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"
)

// stopTask blocks new commands before cancelling active work, including an
// unfinished session start. Failed cleanup leaves a paused task for retry.
func (m *Manager) stopTask(ctx context.Context, s Scope, session string, d *device, automatic bool) error {
	d.mu.Lock()
	t := d.tasks[session]
	if t == nil {
		d.mu.Unlock()
		return m.store.clearTask(ctx, s, session)
	}
	if automatic && (t.paused || t.starting || t.stopping || len(t.calls) > 0 || !t.idle) {
		d.mu.Unlock()
		return nil
	}
	if t.stopping {
		d.mu.Unlock()
		return errors.New("browser task is already ending")
	}
	t.stopping = true
	pauseTask(t)
	if t.lifecycleCancel != nil {
		t.lifecycleCancel()
	}
	d.mu.Unlock()
	defer func() { d.mu.Lock(); t.stopping = false; d.mu.Unlock() }()

	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		d.mu.Lock()
		if d.tasks[session] != t {
			d.mu.Unlock()
			return errors.New("browser task changed while ending")
		}
		busy := t.starting || len(t.calls) > 0
		id, generation := t.id, d.generation
		connected := d.conn != nil && d.ready && time.Now().Before(d.expires)
		d.mu.Unlock()
		if busy {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-tick.C:
				continue
			}
		}
		if id != "" {
			if !connected {
				return errors.New("browser is disconnected; retry ending after reconnection")
			}
			result, err := rpc(ctx, d, "session.stop", map[string]any{"session_id": id, "all": false})
			if err != nil && !sessionGone(err) && !sessionAlreadyStopped(err) {
				return err
			}
			if err == nil {
				var reply struct {
					Stopped []string `json:"stopped"`
				}
				if json.Unmarshal(result, &reply) != nil || !slices.Contains(reply.Stopped, id) {
					return errors.New("BrowserSkill could not finish returning tabs; retry ending the task")
				}
			}
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.tasks[session] != t || d.generation != generation {
			return errors.New("browser connection changed while ending; retry ending the task")
		}
		if err := m.store.clearTask(ctx, s, session); err != nil {
			return err
		}
		delete(d.tasks, session)
		return nil
	}
}

func sessionAlreadyStopped(err error) bool {
	var rpcErr *RPCError
	return errors.As(err, &rpcErr) && rpcErr.Code == "not_found" && rpcErr.Message == "session is not registered"
}
