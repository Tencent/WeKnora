package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClassifyDenialIgnoresSuccess(t *testing.T) {
	d := ClassifyDenial(ExitStatus{Code: 0}, "", "operation not permitted")
	require.Equal(t, DenialNone, d.Reason)
	require.False(t, d.IsDenied())
}

func TestClassifyDenialDetectsOperationNotPermitted(t *testing.T) {
	d := ClassifyDenial(ExitStatus{Code: 1}, "",
		"touch: /Users/dev/outside.txt: Operation not permitted")
	require.Equal(t, DenialOperationNotPermitted, d.Reason)
	require.Equal(t, "/Users/dev/outside.txt", d.Path)
}

func TestClassifyDenialDetectsPermissionDenied(t *testing.T) {
	d := ClassifyDenial(ExitStatus{Code: 1}, "", "bash: /etc/hosts: Permission denied")
	require.Equal(t, DenialPermissionDenied, d.Reason)
}

func TestClassifyDenialDetectsReadOnlyFileSystem(t *testing.T) {
	d := ClassifyDenial(ExitStatus{Code: 1}, "", "cannot create: Read-only file system")
	require.Equal(t, DenialReadOnlyFileSystem, d.Reason)
}

// Shell misuse, permission-bit failures and missing commands share exit codes
// with real denials; excluding them keeps the false-positive rate down.
func TestClassifyDenialIgnoresShellMisuseExitCodes(t *testing.T) {
	for _, code := range []int{2, 126, 127} {
		d := ClassifyDenial(ExitStatus{Code: code}, "", "command not found")
		require.Equal(t, DenialNone, d.Reason, "exit code %d", code)
	}
}

func TestClassifyDenialIgnoresUnrelatedFailure(t *testing.T) {
	d := ClassifyDenial(ExitStatus{Code: 1}, "", "syntax error near unexpected token")
	require.Equal(t, DenialNone, d.Reason)
}

func TestClassifyDenialTreatsKilledAsNotDenied(t *testing.T) {
	d := ClassifyDenial(
		ExitStatus{Code: -1, Killed: true, Duration: time.Second}, "", "")
	require.Equal(t, DenialNone, d.Reason)
}

func TestClassifyDenialTruncatesSnippet(t *testing.T) {
	long := ""
	for i := 0; i < 2000; i++ {
		long += "x"
	}
	d := ClassifyDenial(ExitStatus{Code: 1}, "", long+" Operation not permitted")
	require.LessOrEqual(t, len(d.Snippet), denialSnippetLimit)
}

// curl under NetworkDenied fails at DNS (exit 6) or connect (exit 7), not EPERM.
// ClassifyDenial must leave those alone; Service applies this helper only when
// the policy actually denied the network.
func TestLooksLikeNetworkDenialDetectsCurlDNS(t *testing.T) {
	require.True(t, LooksLikeNetworkDenial(
		ExitStatus{Code: 6},
		"",
		"curl: (6) Could not resolve host: example.com",
	))
}

func TestLooksLikeNetworkDenialDetectsConnectFailure(t *testing.T) {
	require.True(t, LooksLikeNetworkDenial(
		ExitStatus{Code: 7},
		"",
		"curl: (7) Failed to connect to 127.0.0.1 port 9: Couldn't connect to server",
	))
}

func TestLooksLikeNetworkDenialIgnoresUnrelatedExitSix(t *testing.T) {
	require.False(t, LooksLikeNetworkDenial(
		ExitStatus{Code: 1},
		"",
		"syntax error near unexpected token",
	))
}

func TestClassifyRunDenialTreatsBlockedDNSAsDenialWhenNetworkDenied(t *testing.T) {
	p := Policy{Network: NetworkDenied}
	d := ClassifyRunDenial(p, ExitStatus{Code: 6}, "",
		"curl: (6) Could not resolve host: example.com")
	require.Equal(t, DenialPolicy, d.Reason)
}

// The same failure under an unrestricted policy is a real outage, not a block.
func TestClassifyRunDenialIgnoresDNSWhenNetworkAllowed(t *testing.T) {
	p := Policy{Network: NetworkUnrestricted}
	d := ClassifyRunDenial(p, ExitStatus{Code: 6}, "",
		"curl: (6) Could not resolve host: example.com")
	require.Equal(t, DenialNone, d.Reason)
}
