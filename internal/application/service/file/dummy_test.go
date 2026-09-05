package file

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestDummySaveBytesRejectsWriteIntent(t *testing.T) {
	svc := NewDummyFileService()
	called := false
	ctx := interfaces.WithFileWriteIntent(context.Background(), func(context.Context, string) error {
		called = true
		return nil
	})

	path, err := svc.SaveBytes(ctx, []byte("preview"), 7, "preview.docx", false)
	require.Empty(t, path)
	require.ErrorIs(t, err, interfaces.ErrFileWriteIntentUnsupported)
	require.False(t, called)
}
