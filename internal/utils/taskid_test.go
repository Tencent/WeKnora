package utils

import "testing"

func TestParseTaskID_CompoundTaskType(t *testing.T) {
	taskID := GenerateTaskID("faq_import", 42, "kb-abc-123")
	taskType, tenantID, _, uuidPart, businessID, err := ParseTaskID(taskID)
	if err != nil {
		t.Fatalf("ParseTaskID failed: %v", err)
	}
	if taskType != "faq_import" {
		t.Fatalf("taskType = %q, want faq_import", taskType)
	}
	if tenantID != 42 {
		t.Fatalf("tenantID = %d, want 42", tenantID)
	}
	if uuidPart == "" {
		t.Fatal("expected uuid part")
	}
	if businessID == "" {
		t.Fatal("expected business ID")
	}
}

func TestTaskTenantID(t *testing.T) {
	taskID := GenerateTaskID("kb_clone", 7, "source-kb")
	got, err := TaskTenantID(taskID)
	if err != nil {
		t.Fatalf("TaskTenantID failed: %v", err)
	}
	if got != 7 {
		t.Fatalf("TaskTenantID = %d, want 7", got)
	}
}
