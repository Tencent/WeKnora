package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// resolvingCatalog answers Resolve from a fixed handle → tenant map and records
// every Bind, which is all bindContentResources exercises.
type resolvingCatalog struct {
	tenantByRef map[string]uint64
	binds       []bindCall
}

func (c *resolvingCatalog) Resolve(_ context.Context, ref string) (*types.StoredResource, error) {
	tenantID, ok := c.tenantByRef[ref]
	if !ok {
		return nil, errors.New("resource not found")
	}
	handle, _ := types.ParseResourcePath(ref)
	return &types.StoredResource{ID: handle, Handle: handle, TenantID: tenantID}, nil
}

func (c *resolvingCatalog) Bind(_ context.Context, ref, ownerType, ownerID, relation string) error {
	c.binds = append(c.binds, bindCall{ref, ownerType, ownerID, relation})
	return nil
}

func (c *resolvingCatalog) Register(
	context.Context, uint64, string, interfaces.ResourceRegistration,
) (string, error) {
	return "", errors.New("unused")
}

func (c *resolvingCatalog) ResolvePath(
	_ context.Context, value string,
) (string, *types.StoredResource, error) {
	return value, nil, nil
}

func (c *resolvingCatalog) Release(context.Context, string, string, string) (int64, error) {
	return -1, nil
}

func (c *resolvingCatalog) MarkDeleted(context.Context, string) error { return nil }

func (c *resolvingCatalog) CreateAccessGrant(
	context.Context, string, time.Duration,
) (string, error) {
	return "", errors.New("unused")
}

func (c *resolvingCatalog) ResolveAccessGrant(
	context.Context, string,
) (*types.StoredResource, error) {
	return nil, errors.New("unused")
}

func contentRef(char string) string {
	return types.BuildResourcePath(strings.Repeat(char, types.ResourceHandleLength))
}

// Saving a chat answer into the knowledge base must claim the files it shows
// without copying them: the assistant message keeps its own claim, so either
// side can be deleted without breaking the other.
func TestBindContentResourcesClaimsEveryReferencedFile(t *testing.T) {
	chart := contentRef("a")
	table := contentRef("b")
	catalog := &resolvingCatalog{tenantByRef: map[string]uint64{chart: 7, table: 7}}
	svc := &knowledgeService{resourceCatalog: catalog}

	content := "## 结论\n\n![评分](" + chart + ")\n\n数据见 [表格](" + table + ")"
	svc.bindContentResources(context.Background(), 7, "kn-1", content)

	if len(catalog.binds) != 2 {
		t.Fatalf("binds = %v, want one per reference", catalog.binds)
	}
	for i, want := range []string{chart, table} {
		got := catalog.binds[i]
		if got.ref != want {
			t.Fatalf("binds[%d].ref = %q, want %q", i, got.ref, want)
		}
		if got.ownerType != types.ResourceOwnerKnowledge || got.ownerID != "kn-1" {
			t.Fatalf("binds[%d] owner = (%q, %q), want (%q, kn-1)",
				i, got.ownerType, got.ownerID, types.ResourceOwnerKnowledge)
		}
		if got.relation != types.ResourceRelationAttachment {
			t.Fatalf("binds[%d].relation = %q, want %q",
				i, got.relation, types.ResourceRelationAttachment)
		}
	}
}

// A handle pasted from another workspace must not be claimed: binding it would
// hand the caller a file it may not read.
func TestBindContentResourcesSkipsForeignAndUnknownHandles(t *testing.T) {
	mine := contentRef("c")
	theirs := contentRef("d")
	unknown := contentRef("e")
	catalog := &resolvingCatalog{tenantByRef: map[string]uint64{mine: 7, theirs: 9}}
	svc := &knowledgeService{resourceCatalog: catalog}

	content := "![a](" + mine + ") ![b](" + theirs + ") ![c](" + unknown + ")"
	svc.bindContentResources(context.Background(), 7, "kn-1", content)

	if len(catalog.binds) != 1 || catalog.binds[0].ref != mine {
		t.Fatalf("binds = %v, want only %q", catalog.binds, mine)
	}
}

func TestBindContentResourcesIsInertWithoutCatalog(t *testing.T) {
	svc := &knowledgeService{}
	// Must not panic; a deployment without a resource registry has nothing to
	// claim and keeps the pre-binding behaviour.
	svc.bindContentResources(context.Background(), 7, "kn-1", "![a]("+contentRef("f")+")")
}
