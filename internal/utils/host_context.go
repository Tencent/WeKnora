package utils

import "strings"

// HostContextQueryMarker opens the embed host-context envelope. The embed
// widget folds host-injected fields (userId, page path, ...) into the same
// string as the visitor's question — "<marker><field lines>\n\n<query>" — so
// the model can read that context (see buildQueryWithHostContext in the
// frontend). The marker is part of a client/server protocol, so a single
// definition keeps the composer and the consumers in sync.
const HostContextQueryMarker = "[Host context]\n"

// StripHostContextEnvelope returns the text a visitor actually typed by
// dropping the embed host-context envelope it may be prefixed with.
//
// The envelope exists for the model, not for the visitor. Persisting the
// composed query verbatim renders internal fields in the chat bubble and in
// conversation history, and pushes them into the follow-up index. Callers that
// store or display the query pass it through here first; the code path that
// feeds the model keeps the original text.
//
// Text without the envelope is returned unchanged, as is a bare marker that
// never closes with a blank line (nothing to recover in that case). The
// envelope closes on the first blank line, matching the shape the composer
// emits.
func StripHostContextEnvelope(query string) string {
	if !strings.HasPrefix(query, HostContextQueryMarker) {
		return query
	}
	rest := query[len(HostContextQueryMarker):]
	closeAt := strings.Index(rest, "\n\n")
	if closeAt < 0 {
		return query
	}
	return rest[closeAt+2:]
}
