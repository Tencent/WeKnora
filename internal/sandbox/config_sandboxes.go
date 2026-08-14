// Package sandbox: per-config sandbox inventory.
//
// Identity changes (provider / endpoint / API key) and config deletion both
// need one question answered authoritatively: does this config still own live
// sandboxes on the provider?
//
// The binding store cannot answer it. Bindings go missing exactly when leaks
// happen - a CAS rebind overwrites the old sandbox ID, Redis evicts, a process
// restarts - so trusting them under-reports in the one direction that costs
// money. The provider's own listing is the source of truth.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// ConfigSandboxLister is the read half of the provider client.
type ConfigSandboxLister interface {
	List(ctx context.Context, filter RemoteListFilter) ([]RemoteSandboxSummary, error)
}

// ConfigSandboxClient adds deletion, needed to release an inventory.
type ConfigSandboxClient interface {
	ConfigSandboxLister
	Delete(ctx context.Context, sandboxID string) error
}

// NormalizeConfigID maps the empty config ID onto the sentinel used in
// metadata, so "the deployment default config" is addressable like any other.
func NormalizeConfigID(configID string) string {
	if strings.TrimSpace(configID) == "" {
		return types.SandboxConfigIDGlobalDefault
	}
	return configID
}

// MetadataSessionIDKey returns the provider metadata key used for session IDs.
func MetadataSessionIDKey() string {
	return remoteMetadataSessionID
}

// configSandboxFilter narrows a listing to one workspace's one config.
func configSandboxFilter(tenantID uint64, configID string) RemoteListFilter {
	return RemoteListFilter{
		Metadata: map[string]string{
			remoteMetadataTenantID: strconv.FormatUint(tenantID, 10),
			remoteMetadataConfigID: NormalizeConfigID(configID),
		},
		// Paused sandboxes are not idle leftovers: they bill, and a session
		// expects to resume them. Omitting them would report "nothing here"
		// right before the credentials that could delete them are replaced.
		States: []RemoteSandboxState{RemoteStateRunning, RemoteStatePaused},
	}
}

// ListConfigSandboxes returns the sandboxes a config currently owns.
func ListConfigSandboxes(
	ctx context.Context,
	client ConfigSandboxLister,
	tenantID uint64,
	configID string,
) ([]RemoteSandboxSummary, error) {
	if client == nil {
		return nil, fmt.Errorf("sandbox: listing requires a client")
	}
	summaries, err := client.List(ctx, configSandboxFilter(tenantID, configID))
	if err != nil {
		return nil, fmt.Errorf(
			"sandbox: list workspace %d config %q sandboxes: %w",
			tenantID, NormalizeConfigID(configID), err)
	}
	return summaries, nil
}

// ReleaseConfigSandboxes deletes every sandbox the config owns and reports how
// many went away.
//
// This is deliberately NOT exposed as an admin action. Its one caller is the
// sweep that runs immediately after a config's credentials are overwritten,
// using the outgoing credentials still held in memory — the only remaining
// moment those sandboxes can be reached at all. An admin blocked from editing a
// config is instead told to end the owning sessions or create a second config.
//
// One undeletable sandbox does not abort the sweep - partial progress is
// strictly better than none - but the failures ARE returned. Swallowing them
// would leave a paused instance billing on credentials that no longer exist
// anywhere, with no record of which sandbox it was.
func ReleaseConfigSandboxes(
	ctx context.Context,
	client ConfigSandboxClient,
	tenantID uint64,
	configID string,
) (int, error) {
	summaries, err := ListConfigSandboxes(ctx, client, tenantID, configID)
	if err != nil {
		return 0, err
	}
	deleted := 0
	var failures []error
	for _, summary := range summaries {
		if err := client.Delete(ctx, summary.ID); err != nil {
			failures = append(failures, fmt.Errorf("delete sandbox %s: %w", summary.ID, err))
			continue
		}
		deleted++
	}
	return deleted, errors.Join(failures...)
}
