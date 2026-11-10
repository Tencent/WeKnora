package sandbox

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

// fakeConfigSandboxClient records the filters it was asked for and serves a
// fixed inventory, so the test can assert both the query and the deletions.
type fakeConfigSandboxClient struct {
	inventory      []RemoteSandboxSummary
	filters        []RemoteListFilter
	deleted        []string
	deleteFailures map[string]error
}

func (f *fakeConfigSandboxClient) List(
	_ context.Context, filter RemoteListFilter,
) ([]RemoteSandboxSummary, error) {
	f.filters = append(f.filters, filter)
	var out []RemoteSandboxSummary
	for _, s := range f.inventory {
		match := true
		for k, v := range filter.Metadata {
			if s.Metadata[k] != v {
				match = false
				break
			}
		}
		if match {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeConfigSandboxClient) Delete(_ context.Context, sandboxID string) error {
	if err := f.deleteFailures[sandboxID]; err != nil {
		return err
	}
	f.deleted = append(f.deleted, sandboxID)
	return nil
}

func summary(id, tenant, config string) RemoteSandboxSummary {
	return RemoteSandboxSummary{
		ID: id,
		Metadata: map[string]string{
			remoteMetadataTenantID: tenant,
			remoteMetadataConfigID: config,
		},
	}
}

func TestListConfigSandboxesIncludesPausedAndScopesToConfig(t *testing.T) {
	client := &fakeConfigSandboxClient{inventory: []RemoteSandboxSummary{
		summary("sb-1", "7", "cfg-a"),
		summary("sb-2", "7", "cfg-b"),
		summary("sb-3", "8", "cfg-a"),
	}}

	got, err := ListConfigSandboxes(context.Background(), client, 7, "cfg-a")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "sb-1", got[0].ID)

	require.Len(t, client.filters, 1)
	require.Equal(t, "cfg-a", client.filters[0].Metadata[remoteMetadataConfigID])
	require.Equal(t, "7", client.filters[0].Metadata[remoteMetadataTenantID])
	require.ElementsMatch(t,
		[]RemoteSandboxState{RemoteStateRunning, RemoteStatePaused},
		client.filters[0].States,
		"paused sandboxes still bill and are what a session expects to resume")
}

func TestListConfigSandboxesUsesSentinelForGlobalDefault(t *testing.T) {
	client := &fakeConfigSandboxClient{inventory: []RemoteSandboxSummary{
		summary("sb-1", "7", types.SandboxConfigIDGlobalDefault),
	}}

	got, err := ListConfigSandboxes(context.Background(), client, 7, "")
	require.NoError(t, err)
	require.Len(t, got, 1)
}

func TestMetadataSessionIDKeyReturnsSessionMetadataKey(t *testing.T) {
	require.Equal(t, remoteMetadataSessionID, MetadataSessionIDKey())
}

// Releasing one config must not touch a sibling config that happens to use the
// same provider account.
func TestReleaseConfigSandboxesLeavesSiblingConfigAlone(t *testing.T) {
	client := &fakeConfigSandboxClient{inventory: []RemoteSandboxSummary{
		summary("sb-1", "7", "cfg-a"),
		summary("sb-2", "7", "cfg-b"),
	}}

	deleted, err := ReleaseConfigSandboxes(context.Background(), client, 7, "cfg-a")
	require.NoError(t, err)
	require.Equal(t, 1, deleted)
	require.Equal(t, []string{"sb-1"}, client.deleted)
}

func TestReleaseConfigSandboxesContinuesAndReturnsDeleteFailures(t *testing.T) {
	client := &fakeConfigSandboxClient{
		inventory: []RemoteSandboxSummary{
			summary("sb-1", "7", "cfg-a"),
			summary("sb-2", "7", "cfg-a"),
			summary("sb-3", "7", "cfg-a"),
		},
		deleteFailures: map[string]error{
			"sb-2": errors.New("provider delete failed"),
		},
	}

	deleted, err := ReleaseConfigSandboxes(context.Background(), client, 7, "cfg-a")
	require.Error(t, err)
	require.ErrorContains(t, err, "sb-2")
	require.Equal(t, 2, deleted)
	require.Equal(t, []string{"sb-1", "sb-3"}, client.deleted)
}
