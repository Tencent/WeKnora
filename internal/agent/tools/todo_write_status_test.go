package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestTodoWriteRejectsUnknownStepStatuses(t *testing.T) {
	for _, status := range []string{"", "blocked", "Completed"} {
		t.Run(status, func(t *testing.T) {
			args, err := json.Marshal(TodoWriteInput{Steps: []PlanStep{
				{ID: "s1", Description: "Find evidence", Status: status},
			}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := NewTodoWriteTool().Execute(context.Background(), args)
			if err != nil {
				t.Fatalf("unexpected execution error: %v", err)
			}
			if result.Success || !strings.Contains(result.Error, "status") {
				t.Fatalf("expected a status validation failure, got %+v", result)
			}
			if strings.Contains(result.Output, "All tasks completed") {
				t.Fatalf("invalid status was reported as completed: %s", result.Output)
			}
		})
	}
}

func TestTodoWriteReportsSkippedSeparately(t *testing.T) {
	args := json.RawMessage(`{"steps":[{"id":"1","description":"Search","status":"completed"},` +
		`{"id":"2","description":"Read","status":"skipped"}]}`)
	result, err := NewTodoWriteTool().Execute(context.Background(), args)
	if err != nil || !result.Success {
		t.Fatalf("expected success, got %+v, %v", result, err)
	}
	if strings.Contains(result.Output, "All tasks completed") || !strings.Contains(result.Output, "Skipped: 1") {
		t.Fatalf("skipped task misreported: %s", result.Output)
	}
}

func TestTodoWriteValidStatuses(t *testing.T) {
	args := json.RawMessage(`{"steps":[{"id":"1","status":"pending"},{"id":"2","status":"in_progress"},` +
		`{"id":"3","status":"completed"}]}`)
	result, err := NewTodoWriteTool().Execute(context.Background(), args)
	if err != nil || !result.Success || !strings.Contains(result.Output, "2 tasks remaining") {
		t.Fatalf("unexpected result: %+v, %v", result, err)
	}
}

func TestTodoWriteCompletedSteps(t *testing.T) {
	args := json.RawMessage(`{"steps":[{"id":"1","status":"completed"}]}`)
	result, err := NewTodoWriteTool().Execute(context.Background(), args)
	if err != nil || !result.Success || !strings.Contains(result.Output, "All tasks completed") {
		t.Fatalf("unexpected result: %+v, %v", result, err)
	}
}
