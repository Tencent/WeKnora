//go:build !anydoc || !cgo

package anydoc

// The default build links no converter: the anydoc archive is a Rust build
// artifact, and requiring a Rust toolchain to compile WeKnora would be a steep
// price for one optional engine. Everything still compiles and the engine
// registry simply reports the engine as unavailable.

const unavailableReason = "anydoc engine not built into this binary " +
	"(rebuild with `make build-anydoc` / `-tags anydoc`)"

func backendAvailable() bool { return false }

func backendUnavailableReason() string { return unavailableReason }

func backendVersion() string { return "" }

func backendConvert(_ []byte, _ Options) (*Result, error) {
	// Convert checks Available first, so this is only reachable if a caller
	// bypasses it.
	return nil, ErrUnavailable
}
