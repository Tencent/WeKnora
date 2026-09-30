package docparser

import (
	"testing"

	"google.golang.org/grpc/resolver"
)

func TestGRPCDocumentReaderDoesNotChangeGlobalResolver(t *testing.T) {
	previous := resolver.GetDefaultScheme()
	resolver.SetDefaultScheme("passthrough")
	t.Cleanup(func() { resolver.SetDefaultScheme(previous) })

	reader, err := NewGRPCDocumentReader("127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	if got := resolver.GetDefaultScheme(); got != "passthrough" {
		t.Fatalf("DocReader changed global gRPC resolver to %q", got)
	}
}
