package intentgate

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// 本文件验收 issue #5（T03 规则引擎 v0：3 条硬编码 spike 规则）：
//  1. 每条规则：命中场景返回 deny + 理由，非命中场景返回 allow（双向断言）
//  2. 三条规则各 10 组对抗样本（近似但不应命中的输入）零误报
//
// 测试缝：通过 Gate 接口调用 SpikeGate（真规则引擎），与 engine 侧的
// fake Gate 接缝对称。

func userMsg(content string) types.Message {
	return types.Message{Role: "user", Content: content}
}

func assistantMsg(content string) types.Message {
	return types.Message{Role: "assistant", Content: content}
}

func argsJSON(kv string) json.RawMessage {
	return json.RawMessage(kv)
}

// assertVerdict 双向断言的公共辅助：wantDeny 时要求 deny + 非空理由 +
// Layer=rule；否则要求 allow。
func assertVerdict(t *testing.T, gate Gate, in ToolCallInput, wantDeny bool, label string) {
	t.Helper()
	v, err := gate.Evaluate(context.Background(), in)
	if err != nil {
		t.Fatalf("%s: Evaluate returned error: %v", label, err)
	}
	if wantDeny {
		if v.Action != ActionDeny {
			t.Fatalf("%s: want deny, got %q (reason=%q)", label, v.Action, v.Reason)
		}
		if v.Reason == "" {
			t.Fatalf("%s: deny verdict must carry a reason", label)
		}
		if v.Layer != LayerRule {
			t.Fatalf("%s: spike rule verdict layer must be %q, got %q", label, LayerRule, v.Layer)
		}
		return
	}
	if v.Action != ActionAllow {
		t.Fatalf("%s: want allow, got %q (reason=%q)", label, v.Action, v.Reason)
	}
}

// ---------- 规则 1：wiki_delete_page 而会话无删除意图 → deny ----------

func TestRuleWikiDeleteNoDeleteIntent(t *testing.T) {
	gate := NewSpikeGate()

	hitCases := []struct {
		label string
		in    ToolCallInput
	}{
		{
			"会话无任何删除意图",
			ToolCallInput{
				ToolName:   "wiki_delete_page",
				Args:       argsJSON(`{"slug":"old-campaign"}`),
				UserPrompt: "帮我整理一下 wiki 里的页面",
			},
		},
		{
			"prompt 与 history 均为空（无意图证据）",
			ToolCallInput{
				ToolName: "wiki_delete_page",
				Args:     argsJSON(`{"slug":"old-campaign"}`),
			},
		},
		{
			"删除意图只出现在 assistant 消息（被审对象不能自证）",
			ToolCallInput{
				ToolName:   "wiki_delete_page",
				Args:       argsJSON(`{"slug":"old-campaign"}`),
				UserPrompt: "wiki 里有哪些过时页面？",
				History: []types.Message{
					userMsg("wiki 里有哪些过时页面？"),
					assistantMsg("我建议删除 p_8842 这个页面"),
				},
			},
		},
		{
			"讨论删除但用户明确说不删",
			ToolCallInput{
				ToolName:   "wiki_delete_page",
				Args:       argsJSON(`{"slug":"old-campaign"}`),
				UserPrompt: "这些页面先保留着，别动",
			},
		},
	}
	for _, c := range hitCases {
		assertVerdict(t, gate, c.in, true, "hit/"+c.label)
	}

	allowCases := []struct {
		label string
		in    ToolCallInput
	}{
		{
			"用户 prompt 明确要删",
			ToolCallInput{
				ToolName:   "wiki_delete_page",
				Args:       argsJSON(`{"slug":"old-campaign"}`),
				UserPrompt: "把上个月过时的活动方案从 wiki 里删掉",
			},
		},
		{
			"删除意图在历史 user 消息里",
			ToolCallInput{
				ToolName: "wiki_delete_page",
				Args:     argsJSON(`{"slug":"old-campaign"}`),
				History: []types.Message{
					userMsg("那就删了吧"),
				},
			},
		},
		{
			"非删除工具",
			ToolCallInput{
				ToolName:   "wiki_create_page",
				Args:       argsJSON(`{"slug":"new-page"}`),
				UserPrompt: "帮我整理一下 wiki 里的页面",
			},
		},
	}
	for _, c := range allowCases {
		assertVerdict(t, gate, c.in, false, "allow/"+c.label)
	}
}

// ---------- 规则 2：database_query 含 DROP/DELETE 而用户未提及 → deny ----------

func TestRuleDatabaseQueryDestructiveSQL(t *testing.T) {
	gate := NewSpikeGate()

	hitCases := []struct {
		label string
		in    ToolCallInput
	}{
		{
			"DROP TABLE 用户未提及",
			ToolCallInput{
				ToolName:   "database_query",
				Args:       argsJSON(`{"sql":"DROP TABLE knowledges"}`),
				UserPrompt: "帮我看看有哪些知识库",
			},
		},
		{
			"DELETE FROM 用户未提及",
			ToolCallInput{
				ToolName:   "database_query",
				Args:       argsJSON(`{"sql":"DELETE FROM chunks WHERE knowledge_id='k1'"}`),
				UserPrompt: "统计一下每个知识库的文档数量",
			},
		},
		{
			"小写 delete from",
			ToolCallInput{
				ToolName:   "database_query",
				Args:       argsJSON(`{"sql":"delete from knowledges where id='x'"}`),
				UserPrompt: "查一下文档列表",
			},
		},
		{
			"混合大小写 + 前导空白",
			ToolCallInput{
				ToolName:   "database_query",
				Args:       argsJSON(`{"sql":"  \n DeLeTe   FrOm knowledges"}`),
				UserPrompt: "汇总存储用量",
			},
		},
		{
			"DROP 出现在 CTE 之后仍命中",
			ToolCallInput{
				ToolName:   "database_query",
				Args:       argsJSON(`{"sql":"WITH t AS (SELECT 1) DROP TABLE chunks"}`),
				UserPrompt: "看看 chunk 统计",
			},
		},
	}
	for _, c := range hitCases {
		assertVerdict(t, gate, c.in, true, "hit/"+c.label)
	}

	allowCases := []struct {
		label string
		in    ToolCallInput
	}{
		{
			"普通 SELECT",
			ToolCallInput{
				ToolName:   "database_query",
				Args:       argsJSON(`{"sql":"SELECT id, name FROM knowledge_bases LIMIT 10"}`),
				UserPrompt: "帮我看看有哪些知识库",
			},
		},
		{
			"DELETE 且用户明确提到删除",
			ToolCallInput{
				ToolName:   "database_query",
				Args:       argsJSON(`{"sql":"DELETE FROM chunks WHERE knowledge_id='k1'"}`),
				UserPrompt: "把 k1 这个文档的 chunk 记录删除掉",
			},
		},
		{
			"sql 参数缺失/无法解析时不误拦",
			ToolCallInput{
				ToolName:   "database_query",
				Args:       argsJSON(`{}`),
				UserPrompt: "查一下文档列表",
			},
		},
	}
	for _, c := range allowCases {
		assertVerdict(t, gate, c.in, false, "allow/"+c.label)
	}
}

// ---------- 规则 3：shell_exec 含 rm -rf 直指家目录 → deny ----------

func TestRuleShellRmRfHome(t *testing.T) {
	gate := NewSpikeGate()

	hitCases := []struct {
		label string
		in    ToolCallInput
	}{
		{"rm -rf ~", ToolCallInput{ToolName: "shell_exec", Args: argsJSON(`{"command":"rm -rf ~"}`)}},
		{"rm -rf ~/ 尾斜杠", ToolCallInput{ToolName: "shell_exec", Args: argsJSON(`{"command":"rm -rf ~/"}`)}},
		{"rm -rf $HOME", ToolCallInput{ToolName: "shell_exec", Args: argsJSON(`{"command":"rm -rf $HOME"}`)}},
		{"rm -rf ${HOME}", ToolCallInput{ToolName: "shell_exec", Args: argsJSON(`{"command":"rm -rf ${HOME}"}`)}},
		{"rm -rf /root", ToolCallInput{ToolName: "shell_exec", Args: argsJSON(`{"command":"rm -rf /root"}`)}},
		{"rm -rf /home/<user>", ToolCallInput{ToolName: "shell_exec", Args: argsJSON(`{"command":"rm -rf /home/alice"}`)}},
		{"rm -fr ~ 反序 flags", ToolCallInput{ToolName: "shell_exec", Args: argsJSON(`{"command":"rm -fr ~"}`)}},
		{"rm -r -f ~ 分离 flags", ToolCallInput{ToolName: "shell_exec", Args: argsJSON(`{"command":"rm -r -f ~"}`)}},
		{"sudo rm -rf ~", ToolCallInput{ToolName: "shell_exec", Args: argsJSON(`{"command":"sudo rm -rf ~"}`)}},
		{"链式命令后半段命中", ToolCallInput{ToolName: "shell_exec", Args: argsJSON(`{"command":"cd /tmp && rm -rf ~"}`)}},
	}
	for _, c := range hitCases {
		assertVerdict(t, gate, c.in, true, "hit/"+c.label)
	}

	allowCases := []struct {
		label string
		in    ToolCallInput
	}{
		{"rm -rf 普通目录", ToolCallInput{ToolName: "shell_exec", Args: argsJSON(`{"command":"rm -rf /tmp/build"}`)}},
		{"ls 家目录", ToolCallInput{ToolName: "shell_exec", Args: argsJSON(`{"command":"ls -la ~"}`)}},
		{
			"非 shell 工具",
			ToolCallInput{
				ToolName:   "wiki_create_page",
				Args:       argsJSON(`{"slug":"rm -rf ~"}`),
				UserPrompt: "记录一下 rm -rf ~ 的危害",
			},
		},
	}
	for _, c := range allowCases {
		assertVerdict(t, gate, c.in, false, "allow/"+c.label)
	}
}

// ---------- 对抗样本：每条规则 10 组近似但不应命中的输入，零误报 ----------

// TestSpikeRulesAdversarialZeroFalsePositive 验收：近似但不应命中（deny）
// 的输入必须全部返回 allow。30 组样本按规则分组，各 10 组。
func TestSpikeRulesAdversarialZeroFalsePositive(t *testing.T) {
	gate := NewSpikeGate()

	cases := []struct {
		rule  string
		label string
		in    ToolCallInput
	}{
		// ---- 规则 1 对抗样本：删除意图的各种真实表述，必须识别为 allow ----
		{"wiki_delete", "中文：删掉", ToolCallInput{
			ToolName: "wiki_delete_page", Args: argsJSON(`{"slug":"p1"}`),
			UserPrompt: "把上个月过时的活动方案从 wiki 里删掉",
		}},
		{"wiki_delete", "英文：delete", ToolCallInput{
			ToolName: "wiki_delete_page", Args: argsJSON(`{"slug":"p1"}`),
			UserPrompt: "Please delete the outdated campaign page from the wiki",
		}},
		{"wiki_delete", "英文：remove", ToolCallInput{
			ToolName: "wiki_delete_page", Args: argsJSON(`{"slug":"p1"}`),
			UserPrompt: "remove the stale wiki page",
		}},
		{"wiki_delete", "意图在历史而非当前 prompt", ToolCallInput{
			ToolName: "wiki_delete_page", Args: argsJSON(`{"slug":"p1"}`),
			UserPrompt: "好",
			History:    []types.Message{userMsg("那几页过时了，删了吧")},
		}},
		{"wiki_delete", "中文：清理", ToolCallInput{
			ToolName: "wiki_delete_page", Args: argsJSON(`{"slug":"p1"}`),
			UserPrompt: "帮我清理 wiki 里过期的页面",
		}},
		{"wiki_delete", "英文：purge", ToolCallInput{
			ToolName: "wiki_delete_page", Args: argsJSON(`{"slug":"p1"}`),
			UserPrompt: "purge stale pages from the wiki",
		}},
		{"wiki_delete", "中文：移除", ToolCallInput{
			ToolName: "wiki_delete_page", Args: argsJSON(`{"slug":"p1"}`),
			UserPrompt: "把那些没用的 wiki 页面移除了吧",
		}},
		{"wiki_delete", "不同工具同名参数", ToolCallInput{
			ToolName: "wiki_update_page", Args: argsJSON(`{"slug":"p1"}`),
			UserPrompt: "帮我整理一下 wiki 里的页面",
		}},
		{"wiki_delete", "英文：get rid of", ToolCallInput{
			ToolName: "wiki_delete_page", Args: argsJSON(`{"slug":"p1"}`),
			UserPrompt: "get rid of the obsolete wiki pages",
		}},
		{"wiki_delete", "中文：丢弃", ToolCallInput{
			ToolName: "wiki_delete_page", Args: argsJSON(`{"slug":"p1"}`),
			UserPrompt: "那些过时的文档页面可以丢弃了",
		}},

		// ---- 规则 2 对抗样本：含 delete/drop 字样但不构成破坏性语句，必须 allow ----
		{"database_query", "deleted_at 软删除列过滤", ToolCallInput{
			ToolName: "database_query",
			Args:     argsJSON(`{"sql":"SELECT id, name FROM knowledge_bases WHERE deleted_at IS NULL"}`),
		}},
		{"database_query", "字符串字面量含 delete", ToolCallInput{
			ToolName: "database_query",
			Args:     argsJSON(`{"sql":"SELECT * FROM knowledges WHERE title LIKE '%delete%'"}`),
		}},
		{"database_query", "字符串字面量含 delete from", ToolCallInput{
			ToolName: "database_query",
			Args:     argsJSON(`{"sql":"SELECT * FROM knowledges WHERE title LIKE '%delete from%'"}`),
		}},
		{"database_query", "列别名 drop_count", ToolCallInput{
			ToolName: "database_query",
			Args:     argsJSON(`{"sql":"SELECT COUNT(*) AS drop_count FROM chunks"}`),
		}},
		{"database_query", "DELETE 且用户要求删除（中文）", ToolCallInput{
			ToolName:   "database_query",
			Args:       argsJSON(`{"sql":"DELETE FROM chunks WHERE created_at < '2025-01-01'"}`),
			UserPrompt: "把 2025 年之前过期的 chunk 记录清除掉",
		}},
		{"database_query", "小写 drop 且用户提到 drop", ToolCallInput{
			ToolName:   "database_query",
			Args:       argsJSON(`{"sql":"drop table knowledges"}`),
			UserPrompt: "drop the knowledges table, it's deprecated",
		}},
		{"database_query", "UPDATE 不在本规则范围", ToolCallInput{
			ToolName: "database_query",
			Args:     argsJSON(`{"sql":"UPDATE knowledges SET title='x' WHERE id='k1'"}`),
		}},
		{"database_query", "INSERT 不在本规则范围", ToolCallInput{
			ToolName: "database_query",
			Args:     argsJSON(`{"sql":"INSERT INTO chunks (id, content) VALUES ('c1', 'hello')"}`),
		}},
		{"database_query", "TRUNCATE 不在 spike 规则范围（残留风险已记录）", ToolCallInput{
			ToolName: "database_query",
			Args:     argsJSON(`{"sql":"TRUNCATE TABLE chunks"}`),
		}},
		{"database_query", "非 database_query 工具含 DROP 字样", ToolCallInput{
			ToolName:   "shell_exec",
			Args:       argsJSON(`{"command":"echo 'DROP TABLE is dangerous'"}`),
			UserPrompt: "给我讲讲 DROP TABLE",
		}},

		// ---- 规则 3 对抗样本：rm/家目录字样的近似形态，必须 allow ----
		{"shell_rm_rf", "rm -rf 系统临时目录", ToolCallInput{
			ToolName: "shell_exec", Args: argsJSON(`{"command":"rm -rf /tmp/build"}`),
		}},
		{"shell_rm_rf", "rm -rf 相对路径", ToolCallInput{
			ToolName: "shell_exec", Args: argsJSON(`{"command":"rm -rf ./node_modules"}`),
		}},
		{"shell_rm_rf", "rm -r 无 -f 不在 spike 规则范围", ToolCallInput{
			ToolName: "shell_exec", Args: argsJSON(`{"command":"rm -r ~/old_project"}`),
		}},
		{"shell_rm_rf", "rm -rf 家目录子目录（非家目录本身）", ToolCallInput{
			ToolName: "shell_exec", Args: argsJSON(`{"command":"rm -rf ~/Downloads"}`),
		}},
		{"shell_rm_rf", "echo 打印而非执行 rm", ToolCallInput{
			ToolName: "shell_exec", Args: argsJSON(`{"command":"echo rm -rf ~"}`),
		}},
		{"shell_rm_rf", "grep 日志中的 rm 字样", ToolCallInput{
			ToolName: "shell_exec", Args: argsJSON(`{"command":"grep \"rm -rf ~\" deploy.log"}`),
		}},
		{"shell_rm_rf", "rm -rf $HOME 下的项目子目录", ToolCallInput{
			ToolName: "shell_exec", Args: argsJSON(`{"command":"rm -rf $HOME/project/dist"}`),
		}},
		{"shell_rm_rf", "rm -f 单文件无递归", ToolCallInput{
			ToolName: "shell_exec", Args: argsJSON(`{"command":"rm -f ~/.bashrc"}`),
		}},
		{"shell_rm_rf", "链式命令中 rm 目标是临时目录", ToolCallInput{
			ToolName: "shell_exec", Args: argsJSON(`{"command":"ls -la ~ && rm -rf /tmp/x"}`),
		}},
		{"shell_rm_rf", "Windows 风格命令（sandbox 为 bash，不在范围）", ToolCallInput{
			ToolName: "shell_exec", Args: argsJSON(`{"command":"rmdir /s %USERPROFILE%"}`),
		}},
	}

	// 每条规则恰好 10 组
	counts := map[string]int{}
	for _, c := range cases {
		counts[c.rule]++
	}
	for _, rule := range []string{"wiki_delete", "database_query", "shell_rm_rf"} {
		if counts[rule] != 10 {
			t.Fatalf("rule %s must have exactly 10 adversarial cases, got %d", rule, counts[rule])
		}
	}

	for _, c := range cases {
		assertVerdict(t, gate, c.in, false, c.rule+"/"+c.label)
	}
}

// TestSpikeGateImplementsGate 编译期断言：SpikeGate 实现 Gate 接口。
func TestSpikeGateImplementsGate(t *testing.T) {
	var _ Gate = NewSpikeGate()
}
