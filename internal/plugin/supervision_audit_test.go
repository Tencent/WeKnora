package plugin

import (
	"context"
	"testing"
	"time"
)

// TestSupervisionActionsAreAudited verifies that the health supervisor's
// decisions (degradation, restart trigger, restart outcome) land in the audit
// stream, so the supervision trail is retrievable beyond the in-memory state.
func TestSupervisionActionsAreAudited(t *testing.T) {
	sink := &MemoryAuditSink{}
	m := NewManager("")
	m.SetSupervisionAuditSink(sink)

	manifest := Manifest{
		APIVersion:      APIVersionV1,
		ID:              "audit.supervised",
		Name:            "Supervised Plugin",
		Version:         "0.1.0",
		ExtensionType:   ExtensionDataSource,
		ProtocolVersion: "v1",
		Entrypoint:      "./supervised",
	}
	rt := &BuiltinRuntime{
		HealthFunc: func(context.Context) HealthStatus {
			return HealthStatus{State: StateUnhealthy, Message: "simulated degradation"}
		},
	}
	if err := m.Register(manifest, rt); err != nil {
		t.Fatalf("register: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := m.Start(ctx, manifest.ID); err != nil {
		t.Fatalf("start: %v", err)
	}

	cfg := HealthSupervisorConfig{Interval: 10 * time.Millisecond, FailureThreshold: 1, RestartEnabled: true, MaxRestartCount: 2}
	if err := m.StartHealthSupervisor(ctx, cfg); err != nil {
		t.Fatalf("start health supervisor: %v", err)
	}
	defer m.StopHealthSupervisor()

	deadline := time.Now().Add(5 * time.Second)
	sawTriggered, sawCompleted := false, false
	for time.Now().Before(deadline) {
		for _, e := range sink.Snapshot() {
			switch e.Action {
			case "supervision.restart.triggered":
				sawTriggered = true
			case "supervision.restart.completed":
				sawCompleted = true
			}
		}
		if sawTriggered && sawCompleted {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected supervision.restart.triggered and supervision.restart.completed audit events; got %+v", sink.Snapshot())
}

// TestSupervisionUnhealthyAuditedWithoutRestart verifies that a degradation
// that does NOT trigger a restart (restart disabled) is still audited.
func TestSupervisionUnhealthyAuditedWithoutRestart(t *testing.T) {
	sink := &MemoryAuditSink{}
	m := NewManager("")
	m.SetSupervisionAuditSink(sink)

	manifest := Manifest{
		APIVersion:      APIVersionV1,
		ID:              "audit.norestart",
		Name:            "Supervised No-Restart Plugin",
		Version:         "0.1.0",
		ExtensionType:   ExtensionDataSource,
		ProtocolVersion: "v1",
		Entrypoint:      "./supervised-norestart",
	}
	rt := &BuiltinRuntime{
		HealthFunc: func(context.Context) HealthStatus {
			return HealthStatus{State: StateUnhealthy, Message: "simulated degradation"}
		},
	}
	if err := m.Register(manifest, rt); err != nil {
		t.Fatalf("register: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := m.Start(ctx, manifest.ID); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Restart disabled: the degradation must be audited but never restarted.
	cfg := HealthSupervisorConfig{Interval: 10 * time.Millisecond, FailureThreshold: 1, RestartEnabled: false}
	if err := m.StartHealthSupervisor(ctx, cfg); err != nil {
		t.Fatalf("start health supervisor: %v", err)
	}
	defer m.StopHealthSupervisor()

	deadline := time.Now().Add(5 * time.Second)
	sawUnhealthy := false
	for time.Now().Before(deadline) {
		for _, e := range sink.Snapshot() {
			if e.Action == "supervision.unhealthy" {
				sawUnhealthy = true
			}
			if e.Action == "supervision.restart.triggered" {
				t.Fatalf("restart must not be triggered when disabled; got %+v", sink.Snapshot())
			}
		}
		if sawUnhealthy {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected supervision.unhealthy audit event; got %+v", sink.Snapshot())
}
