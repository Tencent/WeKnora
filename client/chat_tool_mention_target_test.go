package client

import (
	"encoding/json"
	"testing"
)

func TestChatMentionTargets(t *testing.T) {
	req := AgentQARequest{Query: "use these tools", MentionedItems: []MentionedItem{{Type: "mcp", Name: "lookup", ServiceID: "crm"}, {Type: "skill", Name: "Review", SkillName: "review-doc"}}}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Items []map[string]string `json:"mentioned_items"`
	}
	if err = json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Items[0]["service_id"] != "crm" || wire.Items[1]["skill_name"] != "review-doc" {
		t.Fatalf("missing mention targets: %s", body)
	}
}
