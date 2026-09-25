package pluginsdk

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// Stream writes a streaming answer: items, checkpoints and progress as NDJSON
// lines, flushed one by one. It is safe for concurrent use.
type Stream struct {
	mu      sync.Mutex
	w       http.ResponseWriter
	flusher http.Flusher
	started bool
	closed  bool
	err     error
}

func newStream(w http.ResponseWriter) *Stream {
	f, _ := w.(http.Flusher)
	return &Stream{w: w, flusher: f}
}

func (s *Stream) write(ev pluginapi.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if s.closed {
		return pluginapi.Errorf(pluginapi.CodeInternal, "stream already ended")
	}
	if !s.started {
		s.w.Header().Set("Content-Type", pluginapi.NDJSONContentType)
		s.w.WriteHeader(http.StatusOK)
		s.started = true
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := s.w.Write(append(b, '\n')); err != nil {
		// The caller went away; stop producing.
		s.err = err
		return err
	}
	if s.flusher != nil {
		s.flusher.Flush()
	}
	return nil
}

func dataEvent(t pluginapi.EventType, v any) (pluginapi.Event, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return pluginapi.Event{}, err
	}
	return pluginapi.Event{Type: t, Data: raw}, nil
}

// Item emits one fetched item. An error means WeKnora stopped listening:
// stop fetching and return.
func (s *Stream) Item(item pluginapi.FetchedItem) error {
	ev, err := dataEvent(pluginapi.EventItem, item)
	if err != nil {
		return err
	}
	return s.write(ev)
}

// Checkpoint emits a resumable cursor. It must be a complete snapshot.
func (s *Stream) Checkpoint(c pluginapi.Cursor) error {
	ev, err := dataEvent(pluginapi.EventCheckpoint, c)
	if err != nil {
		return err
	}
	return s.write(ev)
}

// Progress reports progress for display.
func (s *Stream) Progress(message string) error {
	return s.write(pluginapi.Event{Type: pluginapi.EventProgress, Message: message})
}

// Log forwards a line to WeKnora's logs (level: debug, info, warn, error).
func (s *Stream) Log(level, message string) error {
	return s.write(pluginapi.Event{Type: pluginapi.EventLog, Level: level, Message: message})
}

func (s *Stream) end(cursor *pluginapi.Cursor) {
	if cursor == nil {
		cursor = &pluginapi.Cursor{}
	}
	ev, err := dataEvent(pluginapi.EventEnd, cursor)
	if err == nil {
		err = s.write(ev)
	}
	if err != nil {
		s.fail(err)
		return
	}
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

// fail ends the stream with an error: as an error event once lines went
// out, or as an ordinary error answer before that.
func (s *Stream) fail(err error) {
	s.mu.Lock()
	started, closed := s.started, s.closed
	s.mu.Unlock()
	if closed {
		return
	}
	if !started {
		writeError(s.w, err)
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		return
	}
	_ = s.write(pluginapi.Event{Type: pluginapi.EventError, Error: toError(err)})
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}
