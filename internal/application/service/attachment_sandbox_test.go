package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/attachment"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestAttachmentSandboxProjectionWritesUnchangedFilesOnce(t *testing.T) {
	store := &stagingSandboxManager{}
	doc := &types.TemporaryDocument{ID: "doc", FileType: "pdf", Content: "# Report\n"}
	projection := &attachmentSandboxProjection{
		store: store, sessionID: "session",
		originals: map[string]string{types.AttachmentPath(doc.ID): "inputs/report.pdf"},
	}
	ctx := context.Background()
	description := &attachment.Description{Text: "chart", Detail: "brief", Model: "vision", CreatedAt: time.Now()}
	for range 3 {
		_, err := projection.Document(ctx, doc)
		require.NoError(t, err)
		_, err = projection.Image(ctx, doc, 1, []byte("png"), "image/png", "key", description)
		require.NoError(t, err)
	}
	require.Len(t, store.writes, 3)

	doc.Content = "# Report v2\n"
	_, err := projection.Document(ctx, doc)
	require.NoError(t, err)
	require.Len(t, store.writes, 4)
	require.Equal(t, "# Report v2\n", string(store.files["inputs/report.pdf"+parsedSidecarSuffix]))
}
