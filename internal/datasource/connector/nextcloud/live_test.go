package nextcloud

import (
	"os"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// TestLive is read-only and opt-in. The selected root should be a small test
// folder with at least one supported, non-empty file. No environment values or
// live filenames are printed. Private deployments use the normal SSRF policy.
func TestLive(t *testing.T) {
	for _, key := range []string{
		"NEXTCLOUD_LIVE_URL", "NEXTCLOUD_LIVE_USER",
		"NEXTCLOUD_LIVE_APP_PASSWORD", "NEXTCLOUD_LIVE_ROOT",
	} {
		if os.Getenv(key) == "" {
			t.Skip("set all NEXTCLOUD_LIVE_* variables to enable the read-only live test")
		}
	}
	ds := &types.DataSourceConfig{Credentials: map[string]any{
		"base_url": os.Getenv("NEXTCLOUD_LIVE_URL"), "username": os.Getenv("NEXTCLOUD_LIVE_USER"),
		"app_password": os.Getenv("NEXTCLOUD_LIVE_APP_PASSWORD"),
	}}
	c := NewConnector()
	if err := c.Validate(t.Context(), ds); err != nil {
		t.Fatal("live account validation failed")
	}
	resources, err := c.ListResources(t.Context(), ds, os.Getenv("NEXTCLOUD_LIVE_ROOT"))
	if err != nil {
		t.Fatal("live root listing failed")
	}
	for _, resource := range resources {
		if resource.HasChildren {
			continue
		}
		ds.ResourceIDs = []string{resource.ExternalID}
		items, cur, err := c.FetchIncremental(t.Context(), ds, nil)
		if err != nil || len(items) != 1 || len(items[0].Content) == 0 {
			t.Fatal("live file fetch failed")
		}
		items, _, err = c.FetchIncremental(t.Context(), ds, cur)
		if err != nil || len(items) != 0 {
			t.Fatal("live incremental check failed; keep the test file unchanged")
		}
		return
	}
	t.Fatal("live root must contain a supported, non-empty file")
}
