//go:build !linux || !amd64 || !cgo

package wecom_chat_archive

import (
	"context"
	"strings"
	"testing"
)

func TestDefaultClientReportsSDKUnavailable(t *testing.T) {
	err := NewConnector().Validate(context.Background(), validConfig())
	if err == nil || !strings.Contains(err.Error(), "SDK client is not configured") {
		t.Fatalf("Validate error = %v", err)
	}
}
