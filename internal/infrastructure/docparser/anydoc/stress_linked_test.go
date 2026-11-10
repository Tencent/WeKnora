//go:build anydoc && cgo

package anydoc

import (
	"bytes"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Opt-in sustained mixed-format load through the production WithAssets path.
// Use /usr/bin/time -l (macOS) or -v (Linux) to measure native peak RSS too:
// Go's memory statistics do not include Rust allocations.
func TestConcurrentConversionStress(t *testing.T) {
	setting := os.Getenv("ANYDOC_STRESS_DURATION")
	if setting == "" {
		t.Skip("set ANYDOC_STRESS_DURATION, e.g. 30s")
	}
	duration, err := time.ParseDuration(setting)
	if err != nil || duration <= 0 {
		t.Fatalf("invalid stress duration %q", setting)
	}
	type input struct {
		data []byte
		opts Options
		want *Result
	}
	inputs := make([]input, 0, len(benchmarkDocuments))
	for _, name := range benchmarkDocuments {
		format := strings.Split(name, "/")[0]
		if format == "xls" || format == "xlsb" {
			format = "xlsx"
		}
		i := input{data: readFixture(t, name), opts: Options{Format: format, WithAssets: true}}
		i.want, err = Convert(i.data, i.opts)
		if err != nil || i.want.AssetsError != nil {
			t.Fatalf("preflight %s: %v", name, err)
		}
		inputs = append(inputs, i)
	}
	const workers = 8
	samples := make([][]time.Duration, workers)
	var wg sync.WaitGroup
	start := time.Now()
	deadline := start.Add(duration)
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for n := worker; time.Now().Before(deadline); n++ {
				i := inputs[n%len(inputs)]
				began := time.Now()
				got, err := Convert(i.data, i.opts)
				samples[worker] = append(samples[worker], time.Since(began))
				if err != nil ||
					got.AssetsError != nil ||
					got.Markdown != i.want.Markdown ||
					len(got.Assets) != len(i.want.Assets) {
					t.Errorf("worker %d: inconsistent conversion, err=%v", worker, err)
					return
				}
				for j, asset := range got.Assets {
					if !bytes.Equal(asset.Data, i.want.Assets[j].Data) ||
						asset.Name != i.want.Assets[j].Name {
						t.Errorf("worker %d: inconsistent asset %d", worker, j)
						return
					}
				}
			}
		}(worker)
	}
	wg.Wait()
	elapsed := time.Since(start)
	var latencies []time.Duration
	for _, sample := range samples {
		latencies = append(latencies, sample...)
	}
	if len(latencies) == 0 {
		t.Fatal("no conversions completed")
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	t.Logf("workers=%d conversions=%d elapsed=%s throughput=%.1f docs/s "+
		"p50=%s p95=%s p99=%s GoHeapAlloc=%d GoTotalAlloc=%d",
		workers, len(latencies), elapsed, float64(len(latencies))/elapsed.Seconds(),
		latencies[len(latencies)*50/100], latencies[len(latencies)*95/100], latencies[len(latencies)*99/100],
		memory.HeapAlloc, memory.TotalAlloc)
}
