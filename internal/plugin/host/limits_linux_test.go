//go:build linux

package host

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyLimitsCapsMemory(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("no sleep: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	// A cgroup root we cannot write to is logged, not fatal.
	t.Setenv(envCgroup, filepath.Join(t.TempDir(), "not-a-cgroup"))
	release := applyLimits(cmd.Process.Pid, "acme.test", limits{memoryBytes: 64 << 20, cpuMilli: 500})
	defer release()

	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/limits", cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "Max data size") {
			if !strings.Contains(line, fmt.Sprint(64<<20)) {
				t.Fatalf("limit not applied: %s", line)
			}
			return
		}
	}
	t.Fatal("no data size limit line")
}

func TestSetupCgroupWritesLimits(t *testing.T) {
	// A plain directory stands in for a delegated cgroup: the files are
	// what the kernel would read.
	dir := filepath.Join(t.TempDir(), "plugin-1")
	if err := setupCgroup(dir, 4242, limits{memoryBytes: 1 << 30, cpuMilli: 1500}); err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string]string{
		"memory.max": fmt.Sprint(1 << 30), "cpu.max": "150000 100000", "cgroup.procs": "4242",
	} {
		got, _ := os.ReadFile(filepath.Join(dir, file))
		if string(got) != want {
			t.Errorf("%s = %q, want %q", file, got, want)
		}
	}
}
