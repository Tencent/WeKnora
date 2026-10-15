package tools

import (
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/agent/operations"
)

// Descriptors name a fixed, read-only status adapter, never an arbitrary URL.
// A submitted task without a status API remains unknown, never successful.
type operationTask struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

func operationTasks(
	op operations.Operation, params map[string]json.RawMessage, p operations.Projected,
) []operationTask {
	var tasks []operationTask
	add := func(kind, id string) {
		if id != "" && len(tasks) < 20 {
			tasks = append(tasks, operationTask{kind, id})
		}
	}
	param := func(key string) string { var s string; _ = json.Unmarshal(params[key], &s); return s }
	switch op.ID {
	case "knowledge.move":
		add("move", p.Label("task_id"))
	case "evaluation.start":
		add("evaluation", p.Label("task_id"))
	case "data_source.sync":
		add("sync", p.Label("sync_log_id"))
	case "faq.import_selected":
		add("faq_import", p.Label("task_id"))
	case "knowledge.create_from_url", "knowledge.create_from_attachment", "knowledge.reparse":
		id := p.Label("knowledge_id")
		if id == "" {
			id = param("knowledge_id")
		}
		add("parse", id)
	case "knowledge.create_manual", "knowledge.update_manual":
		if status := p.Label("parse_status"); status != "" && status != "draft" && status != "uninitialized" {
			add("parse", p.Label("knowledge_id"))
		}
	case "knowledge.batch_reparse":
		var ids []string
		_ = json.Unmarshal(params["knowledge_ids"], &ids)
		for _, id := range ids {
			add("parse", id)
		}
	default:
		add("unknown", p.Label("task_id"))
	}
	return tasks
}
