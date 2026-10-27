package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/automation"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	automationClaimBatch = 50
	automationPruneEvery = time.Hour
)

// Start schedules automations until ctx ends. Every instance runs it: due
// automations and queued runs are claimed by compare-and-set in the
// database, so each run executes once however many instances look.
func (s *AutomationService) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(s.cfg.Tick)
		defer ticker.Stop()
		var pruned time.Time
		for {
			s.tick(ctx)
			if time.Since(pruned) >= automationPruneEvery {
				pruned = time.Now()
				s.prune(ctx)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-s.wake:
			}
		}
	}()
}

func (s *AutomationService) tick(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf(ctx, "Automation scheduler panicked: %v", r)
		}
	}()
	// The clock is normalized to UTC: next_run_at is persisted as UTC, and
	// SQLite compares DATETIME as text, so a zoned now (e.g. +08:00) would
	// order future tasks as due.
	now := s.now().UTC()
	steps := []struct {
		name string
		run  func(context.Context, time.Time) error
	}{
		{"claim due automations", s.claimDue},
		{"reconcile waiting runs", s.reconcileAwaiting},
		{"expire abandoned runs", s.expireLeases},
		{"start queued runs", s.startQueued},
	}
	for _, step := range steps {
		if err := step.run(ctx, now); err != nil && ctx.Err() == nil {
			logger.Warnf(ctx, "Automation scheduler: %s: %v", step.name, err)
		}
	}
}

// misfireGrace is how late a due time may be seen and still count as on
// time rather than missed.
func (s *AutomationService) misfireGrace() time.Duration {
	return max(2*s.cfg.Tick, time.Minute)
}

func (s *AutomationService) claimDue(ctx context.Context, now time.Time) error {
	var due []*types.Automation
	if err := s.db.WithContext(ctx).
		Where("enabled = ? AND next_run_at IS NOT NULL AND next_run_at <= ?", true, now).
		Order("next_run_at").Limit(automationClaimBatch).Find(&due).Error; err != nil {
		return err
	}
	for _, a := range due {
		if err := s.claim(ctx, a, now); err != nil {
			logger.Warnf(ctx, "Claim automation %s: %v", a.ID, err)
		}
	}
	return nil
}

// claim advances a past its due time and records the run that time is
// owed, unless another instance did first.
func (s *AutomationService) claim(ctx context.Context, a *types.Automation, now time.Time) error {
	sched, err := automation.Parse(a.Trigger)
	if err != nil {
		return s.pause(ctx, a, "the trigger is no longer valid: "+err.Error())
	}
	dueAt := a.NextRunAt.UTC()
	// Never claim a task before its time, whatever the query matched: the
	// stored text may compare as due while the instant is still future.
	if dueAt.After(now) {
		return nil
	}
	next := sched.Next(now)
	kind := types.AutomationRunSchedule
	if now.Sub(dueAt) > s.misfireGrace() {
		kind = types.AutomationRunMisfire
		if a.MisfirePolicy != types.AutomationPolicyRunOnce {
			kind = ""
		}
	}
	queued := false
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&types.Automation{}).Where("id = ? AND version = ?", a.ID, a.Version).
			Updates(map[string]any{
				"next_run_at": next, "enabled": next != nil, "version": a.Version + 1, "updated_at": now,
			})
		if res.Error != nil || res.RowsAffected == 0 || kind == "" {
			return res.Error
		}
		run := &types.AutomationRun{
			AutomationID: a.ID, TenantID: a.TenantID, ScheduledAt: dueAt, TriggerKind: kind,
			Status: types.AutomationRunQueued,
		}
		if reason, err := s.overlap(tx, a); err != nil {
			return err
		} else if reason != "" {
			run.Status, run.Error, run.FinishedAt = types.AutomationRunSkipped, reason, &now
		}
		ins := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(run)
		if ins.Error != nil {
			return ins.Error
		}
		// A conflicting slot inserted nothing; waking on it would hot-loop
		// the scheduler without any new run to execute.
		queued = ins.RowsAffected > 0 && run.Status == types.AutomationRunQueued
		return nil
	})
	if err == nil && queued {
		s.Wake()
	}
	return err
}

// overlap is why a new run of a must be skipped, or "" when it may queue.
func (s *AutomationService) overlap(tx *gorm.DB, a *types.Automation) (string, error) {
	var live []types.AutomationRun
	if err := tx.Select("status").Where("automation_id = ? AND status IN ?", a.ID, []string{
		types.AutomationRunQueued, types.AutomationRunRunning, types.AutomationRunAwaitingApproval,
	}).Find(&live).Error; err != nil {
		return "", err
	}
	if len(live) == 0 {
		return "", nil
	}
	if a.OverlapPolicy != types.AutomationPolicyQueue {
		return "the previous run was still in progress", nil
	}
	for _, r := range live {
		if r.Status == types.AutomationRunQueued {
			return "a run was already queued", nil
		}
	}
	return "", nil
}

// startQueued claims queued runs while this instance has free slots. A run
// waits while another run of its automation holds it.
func (s *AutomationService) startQueued(ctx context.Context, now time.Time) error {
	free := cap(s.slots) - len(s.slots)
	if free <= 0 {
		return nil
	}
	var queued []*types.AutomationRun
	if err := s.db.WithContext(ctx).Where("status = ?", types.AutomationRunQueued).
		Where(`NOT EXISTS (SELECT 1 FROM automation_runs AS holding
			WHERE holding.automation_id = automation_runs.automation_id AND holding.status IN ? )`,
			[]string{types.AutomationRunRunning, types.AutomationRunAwaitingApproval}).
		Order("resume DESC, scheduled_at").Limit(free * 4).Find(&queued).Error; err != nil {
		return err
	}
	for _, run := range queued {
		select {
		case s.slots <- struct{}{}:
		default:
			return nil
		}
		started, err := s.startRun(ctx, run, now)
		if err != nil || !started {
			<-s.slots
			if err != nil {
				logger.Warnf(ctx, "Start automation run %s: %v", run.ID, err)
			}
			continue
		}
		go s.execute(ctx, run)
	}
	return nil
}

func (s *AutomationService) startRun(ctx context.Context, run *types.AutomationRun, now time.Time) (bool, error) {
	lease := now.Add(s.cfg.Lease)
	updates := map[string]any{
		"status": types.AutomationRunRunning, "owner": s.instance, "lease_until": lease, "updated_at": now,
	}
	if run.StartedAt == nil {
		updates["started_at"] = now
	}
	started := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Take the automation's write lock before reading its runs. A run-row
		// CAS alone cannot serialize two different queued runs. Writing first
		// also avoids a deferred read-to-write transaction upgrade on SQLite.
		locked := tx.Model(&types.Automation{}).Where("id = ?", run.AutomationID).
			UpdateColumn("version", gorm.Expr("version + 1"))
		if locked.Error != nil || locked.RowsAffected == 0 {
			return locked.Error
		}
		var holding int64
		if err := tx.Model(&types.AutomationRun{}).
			Where("automation_id = ? AND id <> ? AND status IN ?", run.AutomationID, run.ID, []string{
				types.AutomationRunRunning, types.AutomationRunAwaitingApproval,
			}).Count(&holding).Error; err != nil {
			return err
		}
		if holding > 0 {
			return nil
		}
		res := tx.Model(&types.AutomationRun{}).
			Where("id = ? AND status = ?", run.ID, types.AutomationRunQueued).Updates(updates)
		started = res.Error == nil && res.RowsAffected > 0
		return res.Error
	})
	if err != nil || !started {
		return false, err
	}
	run.Status, run.Owner, run.LeaseUntil = types.AutomationRunRunning, s.instance, &lease
	if run.StartedAt == nil {
		run.StartedAt = &now
	}
	return true, nil
}

// expireLeases ends runs whose instance stopped renewing them. What their
// tool calls did is unknown, so they are never replayed.
func (s *AutomationService) expireLeases(ctx context.Context, now time.Time) error {
	var stale []*types.AutomationRun
	if err := s.db.WithContext(ctx).Where("status = ? AND lease_until < ?", types.AutomationRunRunning, now).
		Limit(automationClaimBatch).Find(&stale).Error; err != nil {
		return err
	}
	for _, run := range stale {
		ok, err := s.finishRun(ctx, run, []string{types.AutomationRunRunning},
			types.AutomationRunNeedsAttention,
			"the instance executing this run stopped; check the conversation for what it already did",
			func(db *gorm.DB) *gorm.DB { return db.Where("lease_until < ?", now) })
		if err != nil {
			return err
		}
		if ok {
			s.closeAbandonedMessage(ctx, run)
		}
	}
	return nil
}

// closeAbandonedMessage completes the message of a run whose executor died,
// so its conversation does not look generating forever.
func (s *AutomationService) closeAbandonedMessage(ctx context.Context, run *types.AutomationRun) {
	if run.SessionID == "" || run.AssistantMessageID == "" {
		return
	}
	msg, err := s.messages.GetMessage(ctx, run.SessionID, run.AssistantMessageID)
	if err != nil || msg == nil || msg.IsCompleted || msg.RunState == types.MessageRunStateAwaitingInput {
		return
	}
	msg.IsCompleted, msg.UpdatedAt = true, s.now()
	if msg.Content == "" {
		msg.Content = "The run was interrupted."
	}
	if err := s.messages.UpdateMessage(ctx, msg); err != nil {
		logger.Warnf(ctx, "Close message of abandoned automation run %s: %v", run.ID, err)
	}
	if s.streams != nil {
		_ = s.streams.ClearLiveRun(ctx, run.SessionID, run.AssistantMessageID)
	}
}

// reconcileAwaiting moves waiting runs on once their approval is answered,
// and fails those whose approval ended any other way or timed out.
func (s *AutomationService) reconcileAwaiting(ctx context.Context, now time.Time) error {
	if s.broker == nil {
		return nil
	}
	// Keyset pagination visits later approvals even when earlier ones stay
	// open. Offsets would skip rows as reconciled runs leave the waiting set.
	for cursor := ""; ; {
		var waiting []*types.AutomationRun
		if err := s.db.WithContext(ctx).
			Where("status = ? AND id > ?", types.AutomationRunAwaitingApproval, cursor).
			Order("id").Limit(automationClaimBatch * 2).Find(&waiting).Error; err != nil {
			return err
		}
		for _, run := range waiting {
			if err := s.reconcileRun(ctx, run, now); err != nil {
				logger.Warnf(ctx, "Reconcile automation run %s: %v", run.ID, err)
			}
		}
		if len(waiting) < automationClaimBatch*2 {
			return nil
		}
		cursor = waiting[len(waiting)-1].ID
	}
}

func (s *AutomationService) reconcileRun(ctx context.Context, run *types.AutomationRun, now time.Time) error {
	if run.CancelRequested {
		return s.cancelRun(ctx, run, "cancelled by the owner")
	}
	waiting := []string{types.AutomationRunAwaitingApproval}
	prompt, err := s.broker.Store().Get(ctx, run.PendingPromptID)
	if err != nil {
		return err
	}
	if prompt == nil {
		_, err := s.finishRun(ctx, run, waiting, types.AutomationRunFailed, "the approval it waited for is gone", nil)
		return err
	}
	switch {
	case prompt.Status == types.AgentPromptAnswered:
		return s.queueResume(ctx, run.ID, prompt.ID)
	case prompt.IsOpen():
		var a types.Automation
		if err := s.db.WithContext(ctx).Unscoped().Where("id = ?", run.AutomationID).First(&a).Error; err != nil {
			return err
		}
		timeout := time.Duration(a.ApprovalTimeoutMinutes) * time.Minute
		if a.DeletedAt.Valid || now.Sub(run.UpdatedAt) < timeout {
			return nil
		}
		reason := fmt.Sprintf("no approval within %d minutes", a.ApprovalTimeoutMinutes)
		ok, err := s.finishRun(ctx, run, waiting, types.AutomationRunFailed, reason, nil)
		if ok {
			s.closePrompt(ctx, prompt.ID, types.AgentPromptExpired, reason)
		}
		return err
	case prompt.Status == types.AgentPromptExecuting:
		// A resumed run is executing the approved call; it owns the run.
		return nil
	default:
		reason := "the approval ended without an answer"
		if prompt.CloseReason != "" {
			reason = "the approval ended: " + prompt.CloseReason
		}
		_, err := s.finishRun(ctx, run, waiting, types.AutomationRunFailed, reason, nil)
		return err
	}
}

// queueResume hands a waiting run whose approval was answered back to the
// queue, to continue from the answer.
func (s *AutomationService) queueResume(ctx context.Context, runID, promptID string) error {
	res := s.db.WithContext(ctx).Model(&types.AutomationRun{}).
		Where("id = ? AND status = ? AND pending_prompt_id = ? AND cancel_requested = ?",
			runID, types.AutomationRunAwaitingApproval, promptID, false).
		Updates(map[string]any{"status": types.AutomationRunQueued, "resume": true, "updated_at": s.now()})
	if res.Error == nil && res.RowsAffected > 0 {
		s.Wake()
	}
	return res.Error
}

// promptRun is the run of an automation that raised prompt p, if any.
func (s *AutomationService) promptRun(ctx context.Context, p *types.AgentPrompt) (*types.AutomationRun, error) {
	if p == nil || p.SessionID == "" || p.AssistantMessageID == "" {
		return nil, nil
	}
	var run types.AutomationRun
	err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND session_id = ? AND assistant_message_id = ? AND status IN ?",
			p.TenantID, p.SessionID, p.AssistantMessageID, []string{
				types.AutomationRunQueued, types.AutomationRunRunning, types.AutomationRunAwaitingApproval,
			}).First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &run, err
}

// HandlesPrompt claims the prompts automation runs raise, so answering one
// continues the run under the automation instead of as an ordinary chat.
// It fails closed: when it cannot tell, the prompt is left to the scheduler.
func (s *AutomationService) HandlesPrompt(ctx context.Context, p *types.AgentPrompt) bool {
	run, err := s.promptRun(ctx, p)
	if err != nil {
		logger.Warnf(ctx, "Classify prompt %s for automation resume: %v", p.ID, err)
		return true
	}
	if run == nil {
		return false
	}
	if p.Status == types.AgentPromptAnswered && run.Status == types.AutomationRunAwaitingApproval {
		if err := s.queueResume(ctx, run.ID, p.ID); err != nil {
			logger.Warnf(ctx, "Queue automation run %s to resume: %v", run.ID, err)
		}
	}
	return true
}

// RecoverAnswered keeps an answered prompt of an automation run alive until
// the scheduler resumes the run.
func (s *AutomationService) RecoverAnswered(ctx context.Context, p *types.AgentPrompt) (bool, error) {
	run, err := s.promptRun(ctx, p)
	if err != nil || run == nil || s.broker == nil {
		return false, err
	}
	if run.Status == types.AutomationRunAwaitingApproval {
		if err := s.queueResume(ctx, run.ID, p.ID); err != nil {
			return false, err
		}
	}
	until := s.now().Add(30 * time.Second)
	return s.broker.Store().Transition(ctx, p.ID, types.AgentPromptTransition{
		From: []string{types.AgentPromptAnswered}, To: types.AgentPromptAnswered,
		ExpectedOwner: &p.OwnerInstance, LeaseUntil: &until,
	})
}

// finishRun moves run from one of from to status and settles its
// automation. narrow, when set, adds conditions to the update.
func (s *AutomationService) finishRun(
	ctx context.Context, run *types.AutomationRun, from []string, status, reason string,
	narrow func(*gorm.DB) *gorm.DB,
) (bool, error) {
	now := s.now()
	query := s.db.WithContext(context.WithoutCancel(ctx)).Model(&types.AutomationRun{}).
		Where("id = ? AND status IN ?", run.ID, from)
	if narrow != nil {
		query = narrow(query)
	}
	res := query.Updates(map[string]any{
		"status": status, "error": reason, "finished_at": now, "updated_at": now, "lease_until": nil,
	})
	if res.Error != nil || res.RowsAffected == 0 {
		return false, res.Error
	}
	run.Status, run.Error, run.FinishedAt = status, reason, &now
	// Only the winning finalizer may stop work. A stale approval reaper must
	// not cancel a run that another scheduler has already resumed.
	var stopErr error
	if status == types.AutomationRunFailed || status == types.AutomationRunCancelled ||
		status == types.AutomationRunNeedsAttention {
		stopErr = s.stopRunDelegation(context.WithoutCancel(ctx), run)
	}
	s.settle(ctx, run)
	return true, stopErr
}

// settle records a finished run on its automation, pauses it after too many
// failures in a row, and notifies the owner.
func (s *AutomationService) settle(ctx context.Context, run *types.AutomationRun) {
	ctx = context.WithoutCancel(ctx)
	var a types.Automation
	if err := s.db.WithContext(ctx).Where("id = ?", run.AutomationID).First(&a).Error; err != nil {
		return
	}
	updates := map[string]any{"last_run_status": run.Status}
	if run.Status != types.AutomationRunSkipped {
		updates["last_run_at"] = run.ScheduledAt
	}
	failure := run.Status == types.AutomationRunFailed || run.Status == types.AutomationRunNeedsAttention
	switch {
	case run.Status == types.AutomationRunSucceeded:
		updates["last_success_at"] = run.FinishedAt
		updates["consecutive_failures"] = 0
	case failure:
		updates["consecutive_failures"] = gorm.Expr("consecutive_failures + 1")
	}
	if err := s.db.WithContext(ctx).Model(&types.Automation{}).Where("id = ?", a.ID).
		Updates(updates).Error; err != nil {
		logger.Warnf(ctx, "Record run %s on automation %s: %v", run.ID, a.ID, err)
	}
	switch {
	case run.Status == types.AutomationRunSucceeded && a.NotifyOnSuccess:
		s.notify(ctx, &a, run, types.NotificationAutomationSucceeded, run.Answer)
	case failure:
		s.notify(ctx, &a, run, types.NotificationAutomationFailed, run.Error)
		if a.ConsecutiveFailures+1 >= s.cfg.FailuresBeforePause && a.Enabled {
			_ = s.pause(ctx, &a, fmt.Sprintf("%d runs in a row failed", a.ConsecutiveFailures+1))
		}
	}
}

// pause disables a for reason and tells the owner.
func (s *AutomationService) pause(ctx context.Context, a *types.Automation, reason string) error {
	res := s.db.WithContext(context.WithoutCancel(ctx)).Model(&types.Automation{}).
		Where("id = ? AND enabled = ?", a.ID, true).
		Updates(map[string]any{
			"enabled": false, "next_run_at": nil, "paused_reason": reason,
			"version": gorm.Expr("version + 1"), "updated_at": s.now(),
		})
	if res.Error != nil || res.RowsAffected == 0 {
		return res.Error
	}
	s.notify(ctx, a, nil, types.NotificationAutomationPaused, reason)
	return nil
}

func (s *AutomationService) notify(
	ctx context.Context, a *types.Automation, run *types.AutomationRun, kind, body string,
) {
	n := &types.Notification{
		TenantID: a.TenantID, UserID: a.OwnerUserID, Kind: kind, Title: a.Name, Body: body,
		SourceType: "automation", SourceID: a.ID, Link: "/platform/automations/" + a.ID,
	}
	if run != nil {
		n.Link += "?run=" + run.ID
	}
	s.notices.Notify(ctx, n)
}

// prune keeps the newest runs of each automation and drops the runs, and
// per-run conversations, beyond them or of deleted automations.
func (s *AutomationService) prune(ctx context.Context) {
	finished := []string{
		types.AutomationRunSucceeded, types.AutomationRunFailed, types.AutomationRunCancelled,
		types.AutomationRunSkipped, types.AutomationRunNeedsAttention,
	}
	var over []struct {
		AutomationID string
		N            int64
	}
	if err := s.db.WithContext(ctx).Model(&types.AutomationRun{}).Select("automation_id, count(*) AS n").
		Group("automation_id").Having("count(*) > ?", s.cfg.Retention).Scan(&over).Error; err != nil {
		logger.Warnf(ctx, "Prune automation runs: %v", err)
		return
	}
	for _, o := range over {
		var cutoff types.AutomationRun
		if err := s.db.WithContext(ctx).Where("automation_id = ?", o.AutomationID).
			Order("scheduled_at DESC").Offset(s.cfg.Retention).Limit(1).Take(&cutoff).Error; err != nil {
			continue
		}
		old := s.db.Model(&types.AutomationRun{}).
			Where("automation_id = ? AND scheduled_at <= ? AND status IN ?",
				o.AutomationID, cutoff.ScheduledAt, finished)
		s.dropRuns(ctx, old)
	}
	orphaned := s.db.Model(&types.AutomationRun{}).Where("status IN ?", finished).
		Where("automation_id NOT IN (?)", s.db.Model(&types.Automation{}).Select("id"))
	s.dropRuns(ctx, orphaned)
	if err := s.notices.Prune(ctx); err != nil {
		logger.Warnf(ctx, "Prune notifications: %v", err)
	}
}

func (s *AutomationService) dropRuns(ctx context.Context, runs *gorm.DB) {
	var rows []types.AutomationRun
	if err := runs.WithContext(ctx).Select("id, session_id").Limit(1000).Find(&rows).Error; err != nil {
		logger.Warnf(ctx, "Prune automation runs: %v", err)
		return
	}
	if len(rows) == 0 {
		return
	}
	ids, sessions := make([]string, 0, len(rows)), make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
		if r.SessionID != "" {
			sessions = append(sessions, r.SessionID)
		}
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(sessions) > 0 {
			persistent := tx.Model(&types.Automation{}).Unscoped().Select("persistent_session_id").
				Where("persistent_session_id <> ''")
			if err := tx.Where("kind = ? AND id IN ? AND id NOT IN (?)",
				types.SessionKindAutomation, sessions, persistent).
				Delete(&types.Session{}).Error; err != nil {
				return err
			}
		}
		return tx.Where("id IN ?", ids).Delete(&types.AutomationRun{}).Error
	})
	if err != nil {
		logger.Warnf(ctx, "Prune automation runs: %v", err)
	}
}
