package core

import (
	"regexp"
	"strings"
	"time"
)

// ExitStatus is the platform-neutral outcome of one sandboxed process.
type ExitStatus struct {
	Code     int
	Killed   bool
	Duration time.Duration
}

// DenialReason classifies why a sandboxed process is believed to have failed.
type DenialReason int

const (
	// DenialNone means the failure was not classified as a sandbox denial.
	DenialNone DenialReason = iota
	// DenialOperationNotPermitted matches "operation not permitted" in process output.
	DenialOperationNotPermitted
	// DenialPermissionDenied matches "permission denied" in process output.
	DenialPermissionDenied
	// DenialReadOnlyFileSystem matches "read-only file system" in process output.
	DenialReadOnlyFileSystem
	// DenialPolicy matches an explicit sandbox marker or a withheld-network failure.
	DenialPolicy
)

// denialSnippetLimit bounds how much output travels with a denial report.
const denialSnippetLimit = 512

// Denial is the heuristic verdict on whether a failure came from the sandbox.
//
// The kernel reports nothing structured: a denied process simply fails. This
// classification therefore drives UX decisions (show an approval card) and
// must never be used as a security judgement.
type Denial struct {
	Reason DenialReason
	// Path is parsed out of the error text; empty when parsing failed.
	Path    string
	Snippet string
}

// IsDenied reports whether this verdict represents a sandbox denial.
func (d Denial) IsDenied() bool { return d.Reason != DenialNone }

var denialMarkers = []struct {
	needle string
	reason DenialReason
}{
	{"operation not permitted", DenialOperationNotPermitted},
	{"permission denied", DenialPermissionDenied},
	{"read-only file system", DenialReadOnlyFileSystem},
	{"sandbox", DenialPolicy},
}

// denialPathPattern captures the path preceding a known denial marker, e.g.
// "touch: /a/b: Operation not permitted".
var denialPathPattern = regexp.MustCompile(
	`(?i)((?:/|\./|\.\./)[^\s:]*(?:[^\s:]|\\ )*)` +
		`\s*:\s*(?:operation not permitted|permission denied|read-only file system)`)

// ClassifyDenial inspects a failed execution for signs of a sandbox denial.
func ClassifyDenial(status ExitStatus, stdout, stderr string) Denial {
	if status.Killed || status.Code == 0 {
		return Denial{}
	}
	// 2 is shell misuse, 126 is "found but not executable", 127 is "not found".
	switch status.Code {
	case 2, 126, 127:
		return Denial{}
	}

	combined := stderr + "\n" + stdout
	lowered := strings.ToLower(combined)
	for _, marker := range denialMarkers {
		if !strings.Contains(lowered, marker.needle) {
			continue
		}
		return Denial{
			Reason:  marker.reason,
			Path:    firstDenialPath(combined),
			Snippet: truncateSnippet(combined),
		}
	}
	return Denial{}
}

var networkDenialMarkers = []string{
	"could not resolve host",
	"couldn't connect to server",
	"could not connect to server",
	"connection refused",
	"network is unreachable",
}

// LooksLikeNetworkDenial reports DNS/connect failures that Seatbelt produces
// instead of EPERM when network rules are absent. Callers must only consult
// this when Policy.Network is NetworkDenied; a real DNS failure under
// NetworkUnrestricted is not a sandbox denial.
func LooksLikeNetworkDenial(status ExitStatus, stdout, stderr string) bool {
	if status.Killed || status.Code == 0 {
		return false
	}
	switch status.Code {
	case 2, 126, 127:
		return false
	}
	lowered := strings.ToLower(stderr + "\n" + stdout)
	for _, marker := range networkDenialMarkers {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// ClassifyRunDenial is the verdict the Service acts on. Network denials are
// only considered when the policy actually withheld the network, so a genuine
// DNS outage under NetworkUnrestricted is not mistaken for a sandbox block.
func ClassifyRunDenial(p Policy, status ExitStatus, stdout, stderr string) Denial {
	if d := ClassifyDenial(status, stdout, stderr); d.IsDenied() {
		return d
	}
	if p.Network == NetworkDenied && LooksLikeNetworkDenial(status, stdout, stderr) {
		return Denial{Reason: DenialPolicy, Snippet: truncateSnippet(stderr + "\n" + stdout)}
	}
	return Denial{}
}

func firstDenialPath(output string) string {
	match := denialPathPattern.FindStringSubmatch(output)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func truncateSnippet(output string) string {
	trimmed := strings.TrimSpace(output)
	if len(trimmed) <= denialSnippetLimit {
		return trimmed
	}
	return trimmed[len(trimmed)-denialSnippetLimit:]
}
