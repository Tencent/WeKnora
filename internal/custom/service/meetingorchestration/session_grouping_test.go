package meetingorchestration

import (
	"testing"
	"time"
)

func TestGroupCandidateScopesUsesContentContinuity(t *testing.T) {
	first := candidateScope{videoID: "part1", contentText: "确认客户项目范围，下一部分继续讨论审批流", closingText: "下一部分继续讨论审批流", createdAt: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC), videos: []map[string]any{{"video_id": "part1"}}, allowedEvidence: map[string]struct{}{"e1": {}}}
	second := candidateScope{videoID: "part2", contentText: "继续讨论审批流，确认开发负责人和验证方式", openingText: "继续讨论审批流", createdAt: time.Date(2026, 1, 1, 9, 20, 0, 0, time.UTC), videos: []map[string]any{{"video_id": "part2"}}, allowedEvidence: map[string]struct{}{"e2": {}}}

	scopes, sessions, possible := groupCandidateScopes([]candidateScope{second, first}, map[string]evidenceRecord{})
	if len(scopes) != 1 || len(sessions) != 1 {
		t.Fatalf("scopes=%d sessions=%d, want one content group", len(scopes), len(sessions))
	}
	if len(scopes[0].videos) != 2 || len(sessions[0].FragmentVideoIDs) != 2 {
		t.Fatalf("merged videos=%d fragments=%d, want two", len(scopes[0].videos), len(sessions[0].FragmentVideoIDs))
	}
	if possible != 0 {
		t.Fatalf("possible=%d, want 0", possible)
	}
}

func TestGroupCandidateScopesDoesNotMergeWithoutContentEvidence(t *testing.T) {
	first := candidateScope{videoID: "v1", contentText: "讨论客户审批流方案", closingText: "确认方案一", createdAt: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC), videos: []map[string]any{{"video_id": "v1"}}, allowedEvidence: map[string]struct{}{"e1": {}}}
	second := candidateScope{videoID: "v2", contentText: "讨论员工培训预算", openingText: "会议开始确认预算", createdAt: time.Date(2026, 1, 1, 9, 20, 0, 0, time.UTC), videos: []map[string]any{{"video_id": "v2"}}, allowedEvidence: map[string]struct{}{"e2": {}}}

	scopes, sessions, possible := groupCandidateScopes([]candidateScope{first, second}, map[string]evidenceRecord{})
	if len(scopes) != 2 || len(sessions) != 2 {
		t.Fatalf("scopes=%d sessions=%d, want two independent groups", len(scopes), len(sessions))
	}
	if possible != 1 {
		t.Fatalf("possible=%d, want one diagnostic pair", possible)
	}
}

func TestStableSessionAndObjectIDsDoNotDependOnCandidateOrder(t *testing.T) {
	if stableSessionID("part1") != stableSessionID("part1") {
		t.Fatal("session identity is not deterministic")
	}
	if stableClusterID("客户项目", "审批流") != stableClusterID("客户项目", "审批流") {
		t.Fatal("cluster identity is not deterministic")
	}
	if stableWorkItemID("topic-a", "审批流改造") == stableWorkItemID("topic-a", "数据迁移") {
		t.Fatal("different work items share an identity")
	}
}
