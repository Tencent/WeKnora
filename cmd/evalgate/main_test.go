package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIExitCodes(t *testing.T) {
	config := filepath.Join(t.TempDir(), "gate.json")
	if err := os.WriteFile(config, []byte(`{"baseline":{"recall":0.5},"thresholds":{"recall":0.05}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, input string
		code        int
	}{
		{"pass", `{"data":{"metric":{"retrieval_metrics":{"recall":0.5}}}}`, 0},
		{"regression", `{"data":{"metric":{"retrieval_metrics":{"recall":0.42}}}}`, 1},
		{"missing required metric", `{"data":{"metric":{"retrieval_metrics":{"precision":0.5}}}}`, 2},
		{"missing all metrics", `{"data":{}}`, 2},
		{"null metric", `{"data":{"metric":{"retrieval_metrics":{"recall":null}}}}`, 2},
		{"shadowed metric", `{"data":{"metric":{"retrieval_metrics":{"recall":0.1},"generation_metrics":{"recall":1}}}}`, 2},
		{"failed task with stale scores", `{"data":{"task":{"status":3},"metric":{"retrieval_metrics":{"recall":1}}}}`, 2},
		{"running task", `{"data":{"task":{"status":1},"metric":{"retrieval_metrics":{"recall":1}}}}`, 2},
		{"invalid JSON", `{`, 2},
		{"out of range", `{"data":{"metric":{"retrieval_metrics":{"recall":5}}}}`, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, stderr bytes.Buffer
			if code := run([]string{"-config", config}, strings.NewReader(tc.input), &out, &stderr); code != tc.code {
				t.Fatalf("exit=%d want=%d stdout=%s stderr=%s", code, tc.code, &out, &stderr)
			}
		})
	}
}
