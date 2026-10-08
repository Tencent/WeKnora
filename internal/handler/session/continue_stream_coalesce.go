package session

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// coalesceReplayEvents merges each run of consecutive, unfinished delta
// chunks of one segment (same type and event ID; the types are
// deltaResponseTypes, the ones clients accumulate by event_id) into one event
// that carries the run's concatenated content, its last timestamp and the
// union of its data (later keys win). A done chunk, a chunk of another type
// or of another segment ends the run and passes through untouched, so
// completion markers, durations and references keep their own frames and
// the client's per-event_id accumulation ends with the same text.
//
// Events are stored at the granularity the model emits them, so one long
// answer can mean tens of thousands of frames on replay, each an SSE write, a
// flush and a client-side pass (#3362); this brings it down to a few frames
// per segment. The stored events and their data maps are not modified.
func coalesceReplayEvents(events []interfaces.StreamEvent) []interfaces.StreamEvent {
	if len(events) < 2 {
		return events
	}
	out := make([]interfaces.StreamEvent, 0, 16)
	var run strings.Builder // content of the open run, written to out's last event when it ends
	open := false           // whether out's last event is an unfinished run
	ownsData := false       // whether that event's Data is already a private copy
	closeRun := func() {
		if open {
			out[len(out)-1].Content = run.String()
			run.Reset()
			open = false
		}
	}
	for _, evt := range events {
		mergeable := deltaResponseTypes[evt.Type] && !evt.Done
		if open {
			last := &out[len(out)-1]
			if mergeable && last.Type == evt.Type && last.ID == evt.ID {
				run.WriteString(evt.Content)
				last.Timestamp = evt.Timestamp
				if len(evt.Data) > 0 {
					if !ownsData {
						last.Data = copyEventData(last.Data, len(evt.Data))
						ownsData = true
					}
					for key, value := range evt.Data {
						last.Data[key] = value
					}
				}
				continue
			}
			closeRun()
		}
		out = append(out, evt)
		if mergeable {
			run.WriteString(evt.Content)
			open = true
			ownsData = false
		}
	}
	closeRun()
	return out
}

// copyEventData returns a private copy of a stored event's data map with room
// for extra keys; the original may be shared with the store's own buffer.
func copyEventData(data map[string]interface{}, extra int) map[string]interface{} {
	copied := make(map[string]interface{}, len(data)+extra)
	for key, value := range data {
		copied[key] = value
	}
	return copied
}
