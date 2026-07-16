//go:build linux && amd64 && cgo

package wecom_chat_archive

import "testing"

func TestLinuxClientFactoryReturnsArchiveClient(t *testing.T) {
	client := newUnavailableClient(&Config{})
	if client == nil {
		t.Fatal("client is nil")
	}
	_ = client.Close()
}
