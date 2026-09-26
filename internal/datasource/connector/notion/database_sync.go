package notion

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// databaseSync keeps record membership local to each aggregate document.
type databaseSync struct {
	connector  *Connector
	client     *notionClient
	previous   *notionCursor
	editTimes  map[string]time.Time
	visited    map[string]bool
	membership map[string][]string
}

func (s *databaseSync) fetch(ctx context.Context, pages []notionPage) ([]types.FetchedItem, error) {
	s.excludeDeselectedRecords()
	databases := make(map[string]bool)
	for _, page := range pages {
		if page.isDatabase() {
			databases[page.ID] = true
		}
	}
	// Search is not authoritative for database rows: use the complete query
	// result, including when Search lags behind a row's addition or removal.
	for _, page := range pages {
		if !page.isDatabase() && databases[page.Parent.GetParentID()] {
			delete(s.editTimes, page.ID)
		}
	}
	var items []types.FetchedItem
	for _, page := range pages {
		if !page.isDatabase() || s.visited[page.ID] {
			continue
		}
		item, err := s.fetchDatabase(ctx, page)
		if err != nil {
			return nil, err
		}
		if item != nil {
			items = append(items, *item)
		}
	}
	return items, nil
}

func (s *databaseSync) excludeDeselectedRecords() {
	// The initial visited set contains deselected resources. Their old members
	// remain excluded even when Search no longer lists those individual rows.
	for id, records := range s.previous.DatabaseRecords {
		if !s.visited[id] {
			continue
		}
		for _, recordID := range records {
			if _, included := s.editTimes[recordID]; !included {
				s.visited[recordID] = true
			}
		}
	}
}

func (s *databaseSync) fetchDatabase(ctx context.Context, page notionPage) (*types.FetchedItem, error) {
	records, title, queryID, err := s.connector.queryDatabaseRecords(ctx, s.client, page.ID)
	if err != nil {
		return nil, fmt.Errorf("sync database %s: %w", page.ID, err)
	}
	s.visited[page.ID] = true
	if queryID != "" && queryID != page.ID {
		if s.visited[queryID] {
			return nil, nil
		}
		s.visited[queryID] = true
	}
	active := make([]notionPage, 0, len(records))
	ids := make([]string, 0, len(records))
	changed := !page.LastEditedTime.Equal(s.previous.PageEditTimes[page.ID])
	for _, record := range records {
		if record.InTrash {
			continue
		}
		active = append(active, record)
		ids = append(ids, record.ID)
		s.visited[record.ID] = true
		s.editTimes[record.ID] = record.LastEditedTime
		if !record.LastEditedTime.Equal(s.previous.PageEditTimes[record.ID]) {
			changed = true
		}
	}
	slices.Sort(ids)
	previousIDs, known := s.previous.DatabaseRecords[page.ID]
	s.membership[page.ID] = ids
	if !changed && known && slices.Equal(previousIDs, ids) {
		return nil, nil
	}
	return s.connector.buildDatabaseItem(ctx, s.client, page.ID, title, active)
}
