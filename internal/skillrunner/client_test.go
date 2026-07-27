package skillrunner

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestRunnerUnavailableFailsClosed(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	client := NewClient("http://"+address, "credential", 100*time.Millisecond)
	_, err = client.Execute(context.Background(), ExecuteRequest{
		ExecutionID: "exec", TenantID: "1", SkillID: "skill", VersionID: "version", ScriptPath: "run.py",
		ContentHash: strings.Repeat("a", 64),
	})
	if !errors.Is(err, ErrRunnerUnavailable) {
		t.Fatalf("expected ErrRunnerUnavailable, got %v", err)
	}
}
