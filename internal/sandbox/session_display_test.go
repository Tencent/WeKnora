package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWithSessionDisplayPinsTheHeldSeat(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "wk-display")
	require.NoError(t, os.WriteFile(stub, []byte("#!/bin/sh\nprintf '%s\\n' 3\n"), 0o755))

	cmd := exec.Command("bash", "-c", WithSessionDisplay(`printf '%s %s\n' "$DISPLAY" "$WK_SEAT"`))
	cmd.Env = append(os.Environ(),
		"PATH="+dir,
		"WEKNORA_SESSION_ID=sid-A",
		"DISPLAY=:0",
	)
	out, err := cmd.Output()
	require.NoError(t, err)
	require.Equal(t, ":3 3\n", string(out))
}

func TestWithSessionDisplayDoesNotClaimWhenTheSessionHoldsNothing(t *testing.T) {
	dir := t.TempDir()
	// where misses; claim would succeed and leave a marker if it were called.
	marker := filepath.Join(dir, "claimed")
	stub := "#!/bin/sh\n" +
		"if [ \"$1\" = where ]; then exit 1; fi\n" +
		"touch \"$CLAIM_MARKER\"\n" +
		"printf '%s\\n' 2\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "wk-display"), []byte(stub), 0o755))

	cmd := exec.Command("bash", "-c",
		WithSessionDisplay(`printf '%s %s\n' "${DISPLAY-unset}" "${WK_SEAT-unset}"`))
	cmd.Env = append(os.Environ(),
		"PATH="+dir,
		"WEKNORA_SESSION_ID=sid-A",
		"CLAIM_MARKER="+marker,
		"DISPLAY=:0", // a preset DISPLAY must be dropped, not reused
	)
	out, err := cmd.Output()
	require.NoError(t, err)
	require.Equal(t, "unset unset\n", string(out))
	require.NoFileExists(t, marker, "the prefix must never claim a seat itself")
}

func TestWithSessionDisplayLeavesTheCommandAloneWithoutWkDisplay(t *testing.T) {
	cmd := exec.Command("bash", "-c", WithSessionDisplay(`printf '%s\n' untouched`))
	cmd.Env = append(os.Environ(), "PATH=/usr/bin:/bin", "WEKNORA_SESSION_ID=sid-A")
	out, err := cmd.Output()
	require.NoError(t, err)
	require.Equal(t, "untouched\n", string(out))
}
