package host

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// A plugin that prints a line longer than any buffer keeps its output
// drained: a pipe no one reads would block the plugin's next write.
func TestForwardLogsDrainsPastLongLines(t *testing.T) {
	r, w := io.Pipe()
	handshake := make(chan pluginapi.Handshake, 1)
	hsErr := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		forwardLogs(r, "acme.x", handshake, hsErr)
		close(done)
	}()
	hs := pluginapi.Handshake{Protocol: pluginapi.ProtocolVersion, Network: "unix", Address: "/tmp/p.sock"}
	wrote := make(chan error, 1)
	go func() {
		_, err := fmt.Fprintf(w, "%s\nbefore\r\n%s\nafter", strings.Repeat("x", 3<<20), hs.String())
		_ = w.Close()
		wrote <- err
	}()
	select {
	case got := <-handshake:
		if got != hs {
			t.Fatalf("handshake = %+v", got)
		}
	case err := <-hsErr:
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("the handshake after a long line was never read")
	}
	select {
	case err := <-wrote:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the plugin's writes blocked")
	}
	<-done
}
