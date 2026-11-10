package memory

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// This file is the behavioural regression set. Each case is a short sequence
// of turns across separate sessions, ending in an assertion about what the
// next turn's prompt contains. They are written against the service the chat
// path actually calls, so a change that keeps the unit tests green but breaks
// the user-visible behaviour still fails here.
//
// LoCoMo and LongMemEval are deliberately not used: their published scores are
// vendor-run and disagree by tens of points, and neither has a Chinese split
// that matches how this feature is used.

type memoryScenario struct {
	name string
	// userTurns are what the user says, in order, as if across sessions.
	userTurns []string
	// extracted is the distillation the model returns for those turns.
	extracted []map[string]any
	// laterQuery is what the user asks in a new session afterwards.
	laterQuery string
	// wantInPrompt must appear in the memory injected into that later turn.
	wantInPrompt []string
	// wantAbsent must not appear.
	wantAbsent []string
}

func TestCrossSessionMemoryScenarios(t *testing.T) {
	scenarios := []memoryScenario{
		{
			name:      "记住个人画像并在新会话里带上",
			userTurns: []string{"我是做医疗影像的后端工程师，主要写 Go"},
			extracted: []map[string]any{
				{
					"action": "add", "kind": "profile", "topic": "职业",
					"content": "医疗影像方向的后端工程师，主要写 Go",
				},
			},
			laterQuery:   "帮我设计一个接口",
			wantInPrompt: []string{"医疗影像", "后端工程师"},
		},
		{
			name:      "偏好常驻，与问题内容无关也会带上",
			userTurns: []string{"以后回答直接给结论，不要长篇铺垫"},
			extracted: []map[string]any{
				{"action": "add", "kind": "preference", "topic": "回答风格", "content": "回答直接给结论，不要铺垫"},
			},
			laterQuery:   "今天的天气适合跑步吗",
			wantInPrompt: []string{"直接给结论"},
		},
		{
			name: "事实按问题相关性召回，不相关的不进上下文",
			userTurns: []string{
				"我们生产库是 PostgreSQL 17，跑在法兰克福",
				"前端是 Vue 3 加 Vite",
			},
			extracted: []map[string]any{
				{
					"action": "add", "kind": "fact", "topic": "生产数据库",
					"content": "生产库是 PostgreSQL 17，部署在法兰克福",
				},
				{"action": "add", "kind": "fact", "topic": "前端技术栈", "content": "前端是 Vue 3 加 Vite"},
			},
			laterQuery:   "数据库连接池应该配多大",
			wantInPrompt: []string{"PostgreSQL 17"},
			wantAbsent:   []string{"Vue 3"},
		},
		{
			name: "修正矛盾信息后只保留最新的",
			userTurns: []string{
				"我们用的是 MySQL",
				"更正一下，我们上个月已经迁到 PostgreSQL 了",
			},
			extracted: []map[string]any{
				{"action": "add", "kind": "fact", "topic": "在用的数据库", "content": "用的是 MySQL"},
				{"action": "update", "kind": "fact", "topic": "在用的数据库", "content": "已经迁到 PostgreSQL"},
			},
			laterQuery:   "写一段连接数据库的示例代码",
			wantInPrompt: []string{"PostgreSQL"},
			wantAbsent:   []string{"MySQL"},
		},
		{
			name:      "在办事项可以跨会话续接",
			userTurns: []string{"这周在重构订单服务的支付流程，还没弄完"},
			extracted: []map[string]any{
				{
					"action": "add", "kind": "task", "topic": "在做的重构",
					"content": "在重构订单服务的支付流程，尚未完成",
				},
			},
			laterQuery:   "订单服务那个重构接着往下怎么做",
			wantInPrompt: []string{"支付流程"},
		},
		{
			name: "事情做完后不再被召回",
			userTurns: []string{
				"在重构订单服务的支付流程",
				"支付流程重构已经上线了",
			},
			extracted: []map[string]any{
				{"action": "add", "kind": "task", "topic": "在做的重构", "content": "在重构订单服务的支付流程"},
				{"action": "delete", "kind": "task", "topic": "在做的重构", "content": "支付流程重构已完成"},
			},
			laterQuery: "订单服务现在还有什么在做的",
			wantAbsent: []string{"在重构订单服务的支付流程"},
		},
		{
			name:      "一次性的提问不该被记成长期事实",
			userTurns: []string{"Go 的 map 是并发安全的吗"},
			// A well-behaved extraction returns nothing here, which is the
			// normal outcome; the assertion is that we store nothing either.
			extracted:  nil,
			laterQuery: "Go 的 slice 底层是怎么扩容的",
			wantAbsent: []string{"map", "并发安全"},
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			svc, tenantRepo, messages, models, _ := newExtractionHarness(t)
			tenantRepo.set(1, &types.MemoryConfig{Enabled: true, WriteMode: types.MemoryWriteAuto})

			// Replay the turns one distillation at a time, so an update or a
			// delete sees the state its predecessor left behind.
			for i, turn := range scenario.userTurns {
				messages.messages = []*types.Message{{Role: "user", Content: turn}}
				var decisions []map[string]any
				if i < len(scenario.extracted) {
					decisions = scenario.extracted[i : i+1]
				}
				body, err := json.Marshal(map[string]any{"memories": decisions})
				require.NoError(t, err)
				models.response = string(body)

				require.NoError(t, svc.Handle(context.Background(), extractTask(t, types.MemoryExtractPayload{
					TenantID:    1,
					SubjectID:   "web_user:alice",
					SessionID:   "session-" + string(rune('a'+i)),
					MessageID:   "message-" + string(rune('a'+i)),
					ChatModelID: "conversation-model",
				})))
			}

			// A brand new session: nothing but long-term memory carries over.
			laterCtx := enabledCtx(t, tenantRepo, 1, "alice")
			prompt := svc.Recall(laterCtx, scenario.laterQuery).Prompt

			for _, want := range scenario.wantInPrompt {
				require.Contains(t, prompt, want,
					"expected the later turn to carry %q\nprompt was:\n%s", want, prompt)
			}
			for _, absent := range scenario.wantAbsent {
				require.NotContains(t, prompt, absent,
					"did not expect the later turn to carry %q\nprompt was:\n%s", absent, prompt)
			}
		})
	}
}

// TestReadPathMakesNoModelCall pins the cost promise: recall must not add a
// model call to a turn, no matter how many memories the user has.
func TestReadPathMakesNoModelCall(t *testing.T) {
	svc, tenantRepo, _, models, _ := newExtractionHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	for i := 0; i < 30; i++ {
		_, err := svc.Remember(ctx, types.MemoryItem{
			Kind:    types.MemoryKindFact,
			Topic:   "事实" + string(rune('a'+i)),
			Content: "数据库相关的事实 " + string(rune('a'+i)),
		})
		require.NoError(t, err)
	}

	for i := 0; i < 5; i++ {
		require.NotEmpty(t, svc.Recall(ctx, "数据库怎么调优").Prompt)
	}
	require.Zero(t, models.calls, "the read path must not call a model")
}

// TestInjectedMemoryStaysInsideItsBudget keeps a user with a large memory
// space from quietly eating the context window.
func TestInjectedMemoryStaysInsideItsBudget(t *testing.T) {
	svc, tenantRepo, _, _, _ := newExtractionHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	for i := 0; i < 40; i++ {
		_, err := svc.Remember(ctx, types.MemoryItem{
			Kind:    types.MemoryKindPreference,
			Topic:   "偏好" + string(rune('a'+i)),
			Content: strings.Repeat("很长的偏好说明", 8) + string(rune('a'+i)),
		})
		require.NoError(t, err)
		_, err = svc.Remember(ctx, types.MemoryItem{
			Kind:    types.MemoryKindFact,
			Topic:   "事实" + string(rune('a'+i)),
			Content: "数据库" + strings.Repeat("很长的事实说明", 8) + string(rune('a'+i)),
		})
		require.NoError(t, err)
	}

	prompt := svc.Recall(ctx, "数据库怎么调优").Prompt
	require.NotEmpty(t, prompt)
	// Envelope wording aside, the memory content itself must fit in the two
	// declared budgets.
	require.LessOrEqual(t, len([]rune(prompt)),
		types.MemoryBlockRuneBudget+types.MemoryRecallRuneBudget+600,
		"injected memory must stay within its budget")
}
