package outline

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

func (connector *Connector) FetchStream(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor, handler datasource.StreamHandler) (*types.SyncCursor, error) {
	h, ok := handler.(datasource.AcknowledgingStreamHandler)
	if !ok {
		return nil, errors.New("outline_acknowledgement_required")
	}
	s, err := parseCursor(cursor)
	if err != nil {
		return nil, err
	}
	c, err := newClient(config)
	if err != nil {
		return nil, err
	}
	s.Metrics = c.metrics
	ids, err := selectedIDs(config.ResourceIDs)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, errors.New("outline_collection_required")
	}
	instance, err := authenticate(ctx, c)
	if err != nil {
		return nil, err
	}
	if s.Instance.BaseURL != "" && (s.Instance.BaseURL != instance.BaseURL || s.Instance.WorkspaceID != instance.WorkspaceID) {
		return nil, errors.New("outline_instance_changed")
	}
	if s.Instance.ActorID != instance.ActorID || !slices.Equal(s.Selected, ids) {
		full := s.Run != nil && s.Run.ForceFull
		s.Run = newRun(full)
	}
	s.Instance, s.Selected = instance, ids
	selected := map[string]bool{}
	for _, id := range ids {
		selected[id] = true
	}
	for id, old := range s.Documents {
		if !selected[old.CollectionID] {
			delete(s.Documents, id)
			delete(s.PendingUpserts, id)
			delete(s.PendingDeletions, id)
		}
	}
	for id, collectionID := range s.PendingUpserts {
		if !selected[collectionID] {
			delete(s.PendingUpserts, id)
		}
	}
	if s.Run == nil {
		s.Run = newRun(false)
	}
	checkpoint := func() error {
		s.Metrics["pending_upserts"] = int64(len(s.PendingUpserts))
		s.Metrics["pending_deletions"] = int64(len(s.PendingDeletions))
		s.Metrics["deferred"] = 0
		for _, status := range s.PendingDeletions {
			if status == "deferred" {
				s.Metrics["deferred"]++
			}
		}
		cursor := s.cursor()
		raw, err := cursor.ToJSON()
		if err != nil {
			return err
		}
		s.Metrics["cursor_bytes"] = int64(len(raw))
		start := time.Now()
		err = h.Checkpoint(ctx, s.cursor())
		s.Metrics["checkpoint_duration_ms"] = time.Since(start).Milliseconds()
		return err
	}
	if err := checkpoint(); err != nil {
		return nil, err
	}
	warnings := []string{}
	warn := func(code string) {
		if len(warnings) < 100 {
			warnings = append(warnings, code)
		}
	}
	deletionConfig := *config
	if config.SyncDeletions {
		response, err := c.call(ctx, "documents.deleted", map[string]interface{}{"limit": 1, "offset": 0})
		if err == nil {
			var rows []json.RawMessage
			if json.Unmarshal(response.Data, &rows) != nil {
				err = errors.New("outline_response_invalid")
			}
		}
		if err != nil {
			if fatal(err) {
				return s.cursor(), err
			}
			deletionConfig.SyncDeletions = false
			warn("outline_deletion_capability_unavailable")
		}
	}
	complete := true
	unavailable := map[string]bool{}
	for _, id := range ids {
		col, err := collectionInfo(ctx, c, id)
		if err != nil || col.DeletedAt != "" {
			if fatal(err) {
				return s.cursor(), err
			}
			complete = false
			unavailable[id] = true
			warn("outline_scan_incomplete: " + id)
		} else if col.ArchivedAt != "" {
			selected[id] = false
			for documentID, old := range s.Documents {
				if old.CollectionID == id {
					delete(s.Run.Seen, documentID)
				}
			}
		}
	}
	lastCheckpoint, count := time.Now(), 0
	attempted := map[string]string{}
	failDocument := func(d document, err error) error {
		warn(err.Error())
		s.Metrics["emitted"]++
		_, emitErr := h.EmitWithResult(ctx, types.FetchedItem{ExternalID: d.ID, Title: d.Title,
			Metadata: map[string]string{"error": err.Error(), "error_reason_code": err.Error(), "error_reason": err.Error()}})
		return emitErr
	}
	apply := func(d document) error {
		s.Run.Seen[d.ID] = true
		delete(s.PendingDeletions, d.ID)
		if !d.active(selected) {
			delete(s.Run.Seen, d.ID)
			delete(s.PendingUpserts, d.ID)
			return nil
		}
		_, retrying := s.PendingUpserts[d.ID]
		s.PendingUpserts[d.ID] = d.CollectionID
		old := s.Documents[d.ID]
		previousTime, _ := time.Parse(time.RFC3339, old.UpdatedAt)
		currentTime, _ := time.Parse(time.RFC3339, d.UpdatedAt)
		if d.Text == nil || previousTime.After(currentTime) {
			summary := d
			var err error
			d, err = documentInfo(ctx, c, d.ID)
			if err != nil {
				if fatal(err) {
					return err
				}
				return failDocument(summary, err)
			}
		}
		if !d.active(selected) {
			delete(s.Run.Seen, d.ID)
			delete(s.PendingUpserts, d.ID)
			return nil
		}
		currentTime, _ = time.Parse(time.RFC3339, d.UpdatedAt)
		if previousTime.After(currentTime) {
			return failDocument(d, errors.New("outline_response_invalid"))
		}
		item, fp, err := mappedItem(d, instance)
		if err != nil {
			return failDocument(d, err)
		}
		if s.Run.Applied[d.ID] == fp || (!retrying && !s.Run.ForceFull && old.Fingerprint == fp) {
			s.Metrics["unchanged"]++
			delete(s.PendingUpserts, d.ID)
			return nil
		}
		if attempted[d.ID] == fp {
			return nil
		}
		attempted[d.ID] = fp
		if s.Run.ForceFull {
			item.Metadata["source_full_sync"] = "true"
			item.Metadata["source_sync_run_id"] = s.Run.ID
		}
		result, err := h.EmitWithResult(ctx, item)
		s.Metrics["emitted"]++
		if err != nil {
			return err
		}
		if result.Outcome == datasource.ApplyApplied {
			s.Documents[d.ID] = version{CollectionID: d.CollectionID, Fingerprint: fp, UpdatedAt: d.UpdatedAt}
			s.Run.Applied[d.ID] = fp
			delete(s.PendingUpserts, d.ID)
		}
		count++
		if count >= 50 || time.Since(lastCheckpoint) >= 30*time.Second {
			if err := checkpoint(); err != nil {
				return err
			}
			lastCheckpoint, count = time.Now(), 0
		}
		return nil
	}
	// Pending items from a completed collection must also be retried on resumption.
	for id, collectionID := range s.PendingUpserts {
		if unavailable[collectionID] {
			continue
		}
		d, err := documentInfo(ctx, c, id)
		if err != nil {
			if fatal(err) {
				return s.cursor(), err
			}
			warn(err.Error())
			continue
		}
		if err := apply(d); err != nil {
			return s.cursor(), err
		}
	}
	for _, id := range ids {
		if unavailable[id] {
			continue
		}
		if s.Run.Completed[id] {
			continue
		}
		var err error
		if selected[id] {
			var handlerErr error
			err = c.walk(ctx, "documents.list", map[string]interface{}{
				"collectionId": id, "sort": "createdAt", "direction": "ASC", "statusFilter": []string{"published"},
			}, func(rows []json.RawMessage) error {
				for _, raw := range rows {
					d, err := decodeDocument(raw)
					if err != nil {
						return err
					}
					s.Run.Seen[d.ID] = true
					s.Metrics["discovered"]++
					if err := apply(d); err != nil {
						handlerErr = err
						return err
					}
				}
				handlerErr = checkpoint()
				return handlerErr
			})
			if handlerErr != nil {
				return s.cursor(), handlerErr
			}
		}
		if err != nil {
			if fatal(err) {
				return s.cursor(), err
			}
			// A checkpoint or ingest infrastructure error must not be swallowed.
			if ctx.Err() != nil {
				return s.cursor(), ctx.Err()
			}
			complete = false
			warn("outline_scan_incomplete: " + id)
			continue
		}
		s.Run.Completed[id] = true
		if err := checkpoint(); err != nil {
			return s.cursor(), err
		}
	}
	if complete {
		if err := reconcile(ctx, c, &deletionConfig, s, selected, h, warn); err != nil {
			return s.cursor(), err
		}
		s.LastComplete = time.Now().UTC()
		s.Run = nil
	}
	if err := checkpoint(); err != nil {
		return s.cursor(), err
	}
	if len(warnings) > 0 || len(s.PendingUpserts) > 0 {
		if len(warnings) == 0 {
			warn("outline_pending_upserts")
		}
		return s.cursor(), &datasource.PartialFetchError{Details: warnings}
	}
	return s.cursor(), nil
}

func reconcile(ctx context.Context, c *client, config *types.DataSourceConfig, s *state, selected map[string]bool, h datasource.AcknowledgingStreamHandler, warn func(string)) error {
	candidates := map[string]bool{}
	for id := range s.Documents {
		if !s.Run.Seen[id] {
			candidates[id] = true
			if !config.SyncDeletions {
				s.PendingDeletions[id] = "deferred"
			}
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	deleted := map[string]bool{}
	if config.SyncDeletions {
		err := c.walk(ctx, "documents.deleted", map[string]interface{}{}, func(rows []json.RawMessage) error {
			for _, raw := range rows {
				var row struct {
					ID string `json:"id"`
				}
				if json.Unmarshal(raw, &row) != nil {
					return errors.New("outline_response_invalid")
				}
				if candidates[row.ID] {
					deleted[row.ID] = true
				}
			}
			return nil
		})
		if err != nil {
			if fatal(err) {
				return err
			}
			warn("outline_deletion_capability_unavailable")
			return nil
		}
	}
	for id := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		d, err := documentInfo(ctx, c, id)
		confirmed := deleted[id]
		if err == nil {
			if d.active(selected) {
				delete(s.PendingDeletions, id)
				s.PendingUpserts[id] = d.CollectionID
				continue
			}
			confirmed = true
		} else if fatal(err) {
			return err
		}
		// Never infer deletion from 403/404: no deployment-specific error contract is certified.
		if !confirmed {
			warn("outline_deletion_unconfirmed")
			continue
		}
		s.PendingDeletions[id] = "deferred"
		if !config.SyncDeletions {
			continue
		}
		result, err := h.EmitWithResult(ctx, types.FetchedItem{ExternalID: id, IsDeleted: true})
		s.Metrics["emitted"]++
		if err != nil {
			return err
		}
		if result.Outcome == datasource.ApplyApplied {
			delete(s.Documents, id)
			delete(s.PendingUpserts, id)
			delete(s.PendingDeletions, id)
		} else {
			s.PendingDeletions[id] = "pending"
			warn("deletion_failed")
		}
		if err := h.Checkpoint(ctx, s.cursor()); err != nil {
			return err
		}
	}
	return nil
}
