package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// deleteOrder records the steps of an account deletion. The user repo and
// the sandbox releaser share one log so the test can see which ran first.
type deleteOrder struct {
	mu    sync.Mutex
	steps []string
}

func (o *deleteOrder) note(step string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.steps = append(o.steps, step)
}

func (o *deleteOrder) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]string, len(o.steps))
	copy(out, o.steps)
	return out
}

// memUserRepo is the in-memory user store these deletion tests drive.
// DeleteUser removes the row, so a release failure can be told apart from
// a deletion that went through.
type memUserRepo struct {
	interfaces.UserRepository
	users map[string]*types.User
	log   *deleteOrder
}

func (r *memUserRepo) GetUserByID(_ context.Context, id string) (*types.User, error) {
	u, ok := r.users[id]
	if !ok {
		return nil, errors.New("user not found")
	}
	cp := *u
	return &cp, nil
}

func (r *memUserRepo) DeleteUser(_ context.Context, id string) error {
	r.log.note("DeleteUser")
	delete(r.users, id)
	return nil
}

type fakeSandboxReleaser struct {
	log        *deleteOrder
	releaseErr error
}

func (f *fakeSandboxReleaser) ReleaseOwnerAssignments(context.Context, string) error {
	f.log.note("ReleaseOwnerAssignments")
	return f.releaseErr
}

func (f *fakeSandboxReleaser) ReclaimOwner(context.Context, string) {
	f.log.note("ReclaimOwner")
}

func TestDeleteUserAbortsWhenSandboxReleaseFails(t *testing.T) {
	log := &deleteOrder{}
	repo := &memUserRepo{users: map[string]*types.User{"u-1": {ID: "u-1"}}, log: log}
	releaseErr := errors.New("sandbox release failed")
	svc := &userService{userRepo: repo}
	svc.WithSandboxReleaser(&fakeSandboxReleaser{log: log, releaseErr: releaseErr})

	err := svc.DeleteUser(context.Background(), "u-1")
	require.ErrorIs(t, err, releaseErr)
	got, getErr := svc.GetUserByID(context.Background(), "u-1")
	require.NoError(t, getErr)
	require.Equal(t, "u-1", got.ID)
	require.Equal(t, []string{"ReleaseOwnerAssignments"}, log.snapshot())
}

func TestDeleteUserReleasesBeforeDeletingAndReclaimsAfter(t *testing.T) {
	log := &deleteOrder{}
	repo := &memUserRepo{users: map[string]*types.User{"u-1": {ID: "u-1"}}, log: log}
	svc := &userService{userRepo: repo}
	svc.WithSandboxReleaser(&fakeSandboxReleaser{log: log})

	require.NoError(t, svc.DeleteUser(context.Background(), "u-1"))
	require.Eventually(t, func() bool {
		return len(log.snapshot()) == 3
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"ReleaseOwnerAssignments", "DeleteUser", "ReclaimOwner"}, log.snapshot())
	_, err := svc.GetUserByID(context.Background(), "u-1")
	require.Error(t, err)
}
