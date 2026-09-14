package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	cfgJSON = `{"baseline":{"recall":0.5},"thresholds":{"recall":0.05}}`

	inPass       = `{"data":{"metric":{"retrieval_metrics":{"recall":0.5}}}}`
	inRegression = `{"data":{"metric":{"retrieval_metrics":{"recall":0.42}}}}`
	inMissing    = `{"data":{"metric":{"retrieval_metrics":{"precision":0.5}}}}`
	inNoMetrics  = `{"data":{}}`
	inNull       = `{"data":{"metric":{"retrieval_metrics":{"recall":null}}}}`
	inShadowed   = `{"data":{"metric":{"retrieval_metrics":{"recall":0.1},"generation_metrics":{"recall":1}}}}`
	inStale      = `{"data":{"task":{"status":3},"metric":{"retrieval_metrics":{"recall":1}}}}`
	inRunning    = `{"data":{"task":{"status":1},"metric":{"retrieval_metrics":{"recall":1}}}}`
	inBadJSON    = `{`
	inRange      = `{"data":{"metric":{"retrieval_metrics":{"recall":5}}}}`
)

func TestCLIExitCodes(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "gate.json")
	if err := os.WriteFile(cfg, []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, input string
		code        int
	}{
		{"pass", inPass, 0},
		{"regression", inRegression, 1},
		{"missing required metric", inMissing, 2},
		{"missing all metrics", inNoMetrics, 2},
		{"null metric", inNull, 2},
		{"shadowed metric", inShadowed, 2},
		{"failed task with stale scores", inStale, 2},
		{"running task", inRunning, 2},
		{"invalid JSON", inBadJSON, 2},
		{"out of range", inRange, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, stderr bytes.Buffer
			if code := run([]string{"-config", cfg}, strings.NewReader(tc.input), &out, &stderr); code != tc.code {
				t.Fatalf("exit=%d want=%d stdout=%s stderr=%s", code, tc.code, &out, &stderr)
			}
		})
	}
}
