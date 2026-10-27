package im

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type failingIMResourceCatalog struct{ interfaces.ResourceCatalog }

func (s failingIMResourceCatalog) ResolvePath(context.Context, string) (string, *types.StoredResource, error) {
	return "", nil, errors.New("catalog unavailable")
}

type failingIMShareService struct{ interfaces.KBShareService }

func (s failingIMShareService) CheckTenantKBPermission(
	context.Context, string, uint64, types.TenantRole,
) (types.OrgMemberRole, bool, error) {
	return "", false, errors.New("sharing unavailable")
}

func TestIMResourceAccessFailsClosed(t *testing.T) {
	cases := []string{
		"unshared", "binding missing", "knowledge deleted", "borrower removed", "owner removed",
		"organization deleted", "catalog failure", "sharing failure", "missing identity",
		"other selected KB", "no KBs", "explicit KB restriction",
	}
	for _, externalURL := range []string{"", "https://weknora.example"} {
		for _, scenario := range cases {
			t.Run(scenario+externalURL, func(t *testing.T) {
				f := newIMResourceStorageFixture(t)
				t.Setenv("APP_EXTERNAL_URL", externalURL)
				s, _, _, _ := newFullOutputHarness("")
				f.wire(s)
				ctx := f.context()
				var agent *types.CustomAgent
				var kbIDs []string
				switch scenario {
				case "unshared":
					require.NoError(t, f.db.Where("id = ?", "share").Delete(&types.KnowledgeBaseShare{}).Error)
				case "binding missing":
					require.NoError(t, f.db.Where("owner_id = ?", "shared-doc").Delete(&types.ResourceBinding{}).Error)
				case "knowledge deleted":
					require.NoError(t, f.db.Where("id = ?", "shared-doc").Delete(&types.Knowledge{}).Error)
				case "borrower removed", "owner removed":
					id := uint64(7)
					if scenario == "owner removed" {
						id = 9
					}
					require.NoError(t, f.db.Where("tenant_id = ?", id).Delete(&types.OrganizationTenantMember{}).Error)
				case "organization deleted":
					require.NoError(t, f.db.Where("id = ?", "org").Delete(&types.Organization{}).Error)
				case "catalog failure":
					s.resourceCatalog = failingIMResourceCatalog{ResourceCatalog: f.catalog}
				case "sharing failure":
					s.kbShareService = failingIMShareService{}
				case "missing identity":
					ctx = context.Background()
				case "other selected KB":
					agent = &types.CustomAgent{Config: types.CustomAgentConfig{
						KBSelectionMode: "selected", KnowledgeBases: []string{"other-kb"},
					}}
				case "no KBs":
					agent = &types.CustomAgent{Config: types.CustomAgentConfig{KBSelectionMode: "none"}}
				case "explicit KB restriction":
					kbIDs = []string{"other-kb"}
				}
				resource, err := f.catalog.Resolve(ctx, f.refs[2])
				require.NoError(t, err)
				for _, reference := range []string{f.refs[2], resource.PhysicalPath} {
					content := "![private](" + reference + ")"
					out := cleanIMContent(ctx, content, s.newOutboundFileServiceResolver(f.tenant, agent, kbIDs))
					require.Equal(t, content, out, "an answer mentioning a resource cannot authorize access")
				}
				var grants int64
				require.NoError(t, f.db.Model(&types.ResourceAccessGrant{}).Count(&grants).Error)
				require.Zero(t, grants, "denied resources must not mint short-link capabilities")
			})
		}
	}
}

func TestIMResourceAccessRechecksSharingWithCachedStorage(t *testing.T) {
	f := newIMResourceStorageFixture(t)
	s, _, _, _ := newFullOutputHarness("")
	f.wire(s)
	agent := &types.CustomAgent{Config: types.CustomAgentConfig{
		KBSelectionMode: "selected", KnowledgeBases: []string{"shared-kb"},
	}}
	resolver := s.newOutboundFileServiceResolver(f.tenant, agent, nil)
	content := "![shared](" + f.refs[2] + ")"
	first := cleanIMContent(f.context(), content, resolver)
	require.Contains(t, first, "x-oss-signature=")
	require.NoError(t, f.db.Where("id = ?", "share").Delete(&types.KnowledgeBaseShare{}).Error)
	require.Equal(t, content, cleanIMContent(f.context(), content, resolver),
		"a cached storage client must not bypass a revoked share on the next stream update")
}
