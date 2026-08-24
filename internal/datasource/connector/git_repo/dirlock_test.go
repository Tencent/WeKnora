//go:build unix

package git_repo

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLockCloneDirExclusive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "clone")
	unlock, err := lockCloneDir(dir)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}

	done := make(chan struct{})
	go func() {
		unlock2, err := lockCloneDir(dir)
		if err != nil {
			t.Errorf("second lock: %v", err)
			close(done)
			return
		}
		unlock2()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("second locker acquired before unlock")
	case <-time.After(50 * time.Millisecond):
	}
	unlock()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("second locker did not acquire after unlock")
	}
}
