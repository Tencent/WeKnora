package handler

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/providers"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
)

func TestProviderIconDataURIForOfficialPNG(t *testing.T) {
	v, ok := modelruntime.Get(providers.AceDataCloudID)
	if !ok {
		t.Fatal("Ace Data Cloud provider missing")
	}
	uri := iconDataURI(v.Icon)
	if !strings.HasPrefix(uri, "data:image/png;base64,") {
		t.Fatalf("official PNG icon got wrong data URI media type: %q", uri[:min(len(uri), 30)])
	}
	encoded := strings.TrimPrefix(uri, "data:image/png;base64,")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || !bytes.Equal(decoded, v.Icon) {
		t.Fatal("provider icon data URI did not preserve exact brand bytes")
	}
	if !strings.HasPrefix(iconDataURI([]byte("<svg/>")), "data:image/svg+xml;base64,") {
		t.Fatal("existing SVG provider icons changed media type")
	}
}
