package cmdutil

import (
	"io"
	"os"

	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

// OpenInput returns a reader for path. If path == "-", returns stdin
// (iostreams.IO.In). Otherwise opens the file. Caller is responsible
// for closing if needed — for typical "-input <file>"/"--input -" CLI
// patterns the file is fully read before the command exits and OS
// reclaims the FD, so closing is cosmetic.
func OpenInput(path string) (io.Reader, error) {
	if path == "-" {
		return iostreams.IO.In, nil
	}
	return os.Open(path)
}
