package confluence

import (
	"context"
	"fmt"
	"sort"
)

type syncRootKind uint8

const (
	syncWholeSpace syncRootKind = iota
	syncPageSubtree
)

type syncRoot struct {
	ResourceID string
	SpaceID    string
	Space      space
	PageID     string
	Kind       syncRootKind
}

type syncPlan struct{ Roots []syncRoot }

// buildSyncPlan validates selections before any destructive reconciliation and
// removes scopes which are already covered by a selected ancestor.
func (c *Connector) buildSyncPlan(ctx context.Context, client *client, resourceIDs []string) (*syncPlan, error) {
	spaces, err := client.spaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("list Confluence spaces: %w", err)
	}
	byID := make(map[string]space, len(spaces))
	for _, s := range spaces {
		byID[s.ID] = s
	}

	plan := &syncPlan{}
	wholeSpaces := make(map[string]bool)
	pageRoots := make(map[string]syncRoot)
	ancestorPaths := make(map[string][]page)
	for _, resourceID := range resourceIDs {
		ref, err := parseResourceID(resourceID)
		if err != nil {
			return nil, err
		}
		s, ok := byID[ref.SpaceID]
		if !ok {
			return nil, fmt.Errorf("selected Confluence space %s is unavailable; refusing destructive reconciliation", ref.SpaceID)
		}
		if ref.Kind == resourceSpace {
			wholeSpaces[ref.SpaceID] = true
			continue
		}
		// pageAncestors fetches the page itself on Server/DC and Cloud; that is
		// also the authoritative check that a client did not forge its space ID.
		ancestors, err := client.pageAncestors(ctx, s, ref.PageID)
		if err != nil {
			return nil, fmt.Errorf("validate selected Confluence page %s: %w", ref.PageID, err)
		}
		pageRoots[resourceID] = syncRoot{ResourceID: resourceID, SpaceID: ref.SpaceID, Space: s, PageID: ref.PageID, Kind: syncPageSubtree}
		ancestorPaths[resourceID] = ancestors
		for _, ancestor := range ancestors {
			if ancestor.ID == ref.PageID {
				return nil, fmt.Errorf("invalid cyclic Confluence page ancestry for %s", ref.PageID)
			}
		}
	}
	spaceIDs := make([]string, 0, len(wholeSpaces))
	for id := range wholeSpaces {
		spaceIDs = append(spaceIDs, id)
	}
	sort.Strings(spaceIDs)
	for _, id := range spaceIDs {
		plan.Roots = append(plan.Roots, syncRoot{ResourceID: id, SpaceID: id, Space: byID[id], Kind: syncWholeSpace})
	}
	pageIDs := make([]string, 0, len(pageRoots))
	for id := range pageRoots {
		pageIDs = append(pageIDs, id)
	}
	sort.Strings(pageIDs)
	for _, id := range pageIDs {
		root := pageRoots[id]
		if wholeSpaces[root.SpaceID] {
			continue
		}
		ancestors := ancestorPaths[id]
		covered := false
		for _, ancestor := range ancestors {
			if ancestor.Kind != "" && ancestor.Kind != "page" {
				continue
			}
			if _, ok := pageRoots[makePageResourceID(root.SpaceID, ancestor.ID)]; ok {
				covered = true
				break
			}
		}
		if !covered {
			plan.Roots = append(plan.Roots, pageRoots[id])
		}
	}
	return plan, nil
}
