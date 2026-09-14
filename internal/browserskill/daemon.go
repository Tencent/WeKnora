package browserskill

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// One native process per app instance. Devices only own their connection and tasks.
type daemon struct {
	home string
	port int
	cmd  *exec.Cmd
	done chan struct{}
}

func connectionLimit() int {
	if n, err := strconv.Atoi(os.Getenv("BROWSERSKILL_MAX_CONNECTIONS")); err == nil && n > 0 {
		return n
	}
	return 32
}

func (d *daemon) exited() bool {
	select {
	case <-d.done:
		return true
	default:
		return false
	}
}

func (d *daemon) stop() {
	_ = d.cmd.Process.Kill()
	<-d.done
	_ = os.RemoveAll(d.home)
}

// Only startup waiters block. Status, existing tools and revocations never wait
// on process startup under the manager mutex.
func (m *Manager) ensureDaemon(ctx context.Context) (*daemon, error) {
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, errors.New("browser manager is closed")
		}
		if m.daemon != nil && !m.daemon.exited() {
			d := m.daemon
			m.mu.Unlock()
			return d, nil
		}
		if pending := m.starting; pending != nil {
			m.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-pending:
				continue
			}
		}
		pending := make(chan struct{})
		m.starting = pending
		previous := m.daemon
		m.daemon = nil
		m.mu.Unlock()
		if previous != nil {
			previous.stop()
		}
		d, err := m.start(ctx)
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			if d != nil {
				d.stop()
			}
			m.mu.Lock()
			err = errors.New("browser manager is closed")
		} else if err == nil {
			m.daemon = d
		}
		m.starting = nil
		close(pending)
		m.mu.Unlock()
		return d, err
	}
}

// Close disconnects all users and stops the single process owned by this manager.
func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	for _, d := range m.devices {
		d.mu.Lock()
		disconnectDeviceLocked(d)
		d.expires = time.Time{}
		d.mu.Unlock()
	}
	m.devices = map[string]*device{}
	runtime, pending := m.daemon, m.starting
	m.daemon = nil
	m.mu.Unlock()
	if m.store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = m.store.releaseOwner(ctx, m.nodeID)
		cancel()
	}
	if runtime != nil {
		runtime.stop()
	}
	if pending != nil {
		<-pending
	}
}

func (m *Manager) start(ctx context.Context) (*daemon, error) {
	home, err := os.MkdirTemp("/tmp", "wkb-")
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(
		m.binary,
		"daemon",
		"start",
		"--foreground",
		"--port",
		"0",
		"--daemon-idle",
		"24h",
		"--session-idle",
		"30m",
	)
	cmd.Env = append(os.Environ(), "BSK_HOME="+home)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		_ = os.RemoveAll(home)
		return nil, errors.New("could not start BrowserSkill daemon")
	}
	d := &daemon{home: home, cmd: cmd, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(d.done) }()
	started := false
	defer func() {
		if !started {
			_ = cmd.Process.Kill()
			<-d.done
			_ = os.RemoveAll(home)
		}
	}()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return nil, ctx.Err()
		case <-d.done:
			return nil, errors.New("BrowserSkill daemon exited during startup")
		case <-timer.C:
			_ = cmd.Process.Kill()
			return nil, errors.New("BrowserSkill daemon startup timed out")
		case <-tick.C:
			data, e := os.ReadFile(filepath.Join(home, "daemon.json"))
			if e != nil {
				continue
			}
			var info struct {
				Port int `json:"ws_port"`
			}
			if json.Unmarshal(data, &info) == nil && info.Port > 0 {
				d.port = info.Port
				started = true
				return d, nil
			}
		}
	}
}
