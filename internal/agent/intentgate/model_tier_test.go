package intentgate

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func chatModelWith(source types.ModelSource, ctxWindow int, extra map[string]string) *types.Model {
	return &types.Model{
		ID:     "m-1",
		Name:   "test-chat",
		Type:   types.ModelTypeKnowledgeQA,
		Source: source,
		Parameters: types.ModelParameters{
			ContextWindow: ctxWindow,
			ExtraConfig:   extra,
		},
	}
}

// TestJudgeCapableExplicitOverride：ExtraConfig 显式开关优先级最高，
// 连本地模型也能被显式打开，远程模型也能被显式关掉。
func TestJudgeCapableExplicitOverride(t *testing.T) {
	cases := []struct {
		name  string
		model *types.Model
		want  bool
	}{
		{"nil model 按弱档", nil, false},
		{"显式 true 打开本地模型", chatModelWith(types.ModelSourceLocal, 0, map[string]string{
			JudgeCapableExtraConfigKey: "true"}), true},
		{"显式 on 等价 true", chatModelWith(types.ModelSourceOpenAI, 0, map[string]string{
			JudgeCapableExtraConfigKey: "on"}), true},
		{"显式 false 关掉远程模型", chatModelWith(types.ModelSourceOpenAI, 0, map[string]string{
			JudgeCapableExtraConfigKey: "false"}), false},
		{"显式 off 等价 false", chatModelWith(types.ModelSourceGemini, 0, map[string]string{
			JudgeCapableExtraConfigKey: "off"}), false},
		{"未识别取值回落启发式（远程→强档）", chatModelWith(types.ModelSourceOpenAI, 0, map[string]string{
			JudgeCapableExtraConfigKey: "maybe"}), true},
	}
	for _, tc := range cases {
		if got := JudgeCapable(tc.model); got != tc.want {
			t.Fatalf("%s: JudgeCapable = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestJudgeCapableContextWindow：上下文窗口已知且装不下 judge 输入预算
// （< 8k）→ 弱档，即使远程商业模型。
func TestJudgeCapableContextWindow(t *testing.T) {
	if JudgeCapable(chatModelWith(types.ModelSourceOpenAI, 4096, nil)) {
		t.Fatal("4k 上下文窗口应判弱档（装不下 judge 输入预算）")
	}
	if !JudgeCapable(chatModelWith(types.ModelSourceOpenAI, 32768, nil)) {
		t.Fatal("32k 上下文窗口应判强档")
	}
	// 未知窗口（0）不因此判弱档，由来源启发式决定。
	if !JudgeCapable(chatModelWith(types.ModelSourceOpenAI, 0, nil)) {
		t.Fatal("远程模型未知窗口应判强档")
	}
}

// TestJudgeCapableSourceHeuristic：本地模型默认弱档（小参数本地模型的
// 指令遵循不足以承担安全判定）；远程商业 API 默认强档。
func TestJudgeCapableSourceHeuristic(t *testing.T) {
	if JudgeCapable(chatModelWith(types.ModelSourceLocal, 131072, nil)) {
		t.Fatal("本地模型即使窗口大也应默认弱档（除非显式打开）")
	}
	for _, src := range []types.ModelSource{
		types.ModelSourceOpenAI, types.ModelSourceGemini, types.ModelSourceDeepseek,
		types.ModelSourceAliyun, types.ModelSourceOpenRouter,
	} {
		if !JudgeCapable(chatModelWith(src, 0, nil)) {
			t.Fatalf("远程模型 %s 应默认强档", src)
		}
	}
}
