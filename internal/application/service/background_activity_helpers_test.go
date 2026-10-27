package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/background"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// liveActivityRegistry returns an in-memory registry holding one live
// background item for each ref.
func liveActivityRegistry(t *testing.T, refs ...interfaces.SessionRef) *background.MemoryActivityRegistry {
	t.Helper()
	reg := background.NewMemoryActivityRegistry(nil)
	for i, ref := range refs {
		require.NoError(t, reg.Add(context.Background(), ref, interfaces.BackgroundActivity{
			ID: fmt.Sprintf("sj_%d", i), Kind: "shell_job", Title: "build", StartedAt: time.Now(),
		}, time.Hour, 0))
	}
	return reg
}

// failingActivityRegistry fails every List call.
type failingActivityRegistry struct {
	interfaces.ActivityRegistry
	err error
}

func (r failingActivityRegistry) List(context.Context, interfaces.SessionRef) ([]interfaces.BackgroundActivity, error) {
	return nil, r.err
}
