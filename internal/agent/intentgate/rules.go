// 规则引擎 v0：3 条硬编码 spike 规则（P0 spike，设计 §13）。
//
// 本文件实现 IntentGate 判定漏斗的①确定性规则层的最小形态：
// 无策略表、无 judge，3 条硬编码规则覆盖三类高危工具调用的意图对齐检查。
// 全部 verdict 只承担 observe 语义——是否拦截由 engine 接缝（act.go）
// 按策略 mode 决定，现阶段（T02）任何 verdict 都只记录不拦截。
//
// 已知边界（spike 有意不覆盖，见 issue #5 评论与后续 ticket）：
//   - SQL 破坏性语句只识别 DROP/DELETE，不含 TRUNCATE/ALTER/UPDATE...WHERE 缺失；
//   - rm 只识别 r+f 组合直指家目录本身，不含 rm -r、家目录子目录、~user 形态；
//   - 意图识别是中英关键词匹配，指代与反讽等语义判断留给语义层（judge）。
package intentgate

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// 工具名字面量与 internal/agent/tools/definitions.go 的常量保持一致
// （ToolWikiDeletePage / ToolDatabaseQuery / ToolShellExec）。此处不 import
// tools 包，避免 intentgate 对工具注册层的反向依赖。
const (
	spikeToolWikiDeletePage = "wiki_delete_page"
	spikeToolDatabaseQuery  = "database_query"
	spikeToolShellExec      = "shell_exec"
)

// Rule 是一条确定性规则：对一次工具调用做纯内存判定，
// 命中返回 hit=true 与判定理由。规则之间短路，先命中先生效。
type Rule struct {
	// ID 稳定标识，写入 Verdict.Reason 前缀，供 verdict 日志对账。
	ID string
	// Description 规则的人类可读说明（NLC 原文的 spike 形态）。
	Description string
	// Match 判定函数。实现必须是纯函数：无 IO、无网络、无 panic。
	Match func(in ToolCallInput) (hit bool, reason string)
}

// RuleEngine ① 确定性规则层：按注册顺序评估规则，第一条命中的
// 规则产出 deny verdict；全部不命中返回 allow（设计 §8.1：spike 期
// 没有 risk_tier=high 策略需要升级语义层，不命中即 allow）。
type RuleEngine struct {
	rules []Rule
}

// NewSpikeRuleEngine 返回注册了三条 spike 规则的规则引擎。
func NewSpikeRuleEngine() *RuleEngine {
	return &RuleEngine{rules: []Rule{
		ruleWikiDeleteNoIntent(),
		ruleDatabaseQueryDestructiveSQL(),
		ruleShellRmRfHome(),
	}}
}

// Evaluate 评估全部规则，返回首个命中规则的 deny verdict；
// 无命中返回 allow。命中 verdict 的 Layer=rule、PolicyID 为空
// （spike 无策略表，设计 §6.2：policy_id null = 基线判定）。
func (e *RuleEngine) Evaluate(in ToolCallInput) Verdict {
	for _, r := range e.rules {
		hit, reason := r.Match(in)
		if hit {
			return Verdict{
				Action: ActionDeny,
				Layer:  LayerRule,
				Reason: fmt.Sprintf("%s: %s", r.ID, reason),
			}
		}
	}
	return Verdict{Action: ActionAllow}
}

// SpikeGate 是规则引擎 v0 的 Gate 实现：仅含①规则层，
// 无 PolicyStore/Judge/VerdictWriter（后续 ticket 接入）。
// 它永不返回 error——规则层失败语义由完整 Gate 实现按设计 §9 处理。
type SpikeGate struct {
	engine *RuleEngine
}

// NewSpikeGate 返回加载三条 spike 规则的 Gate。
func NewSpikeGate() *SpikeGate {
	return &SpikeGate{engine: NewSpikeRuleEngine()}
}

// Evaluate 实现 Gate 接口，委托给内部规则引擎。
func (g *SpikeGate) Evaluate(_ context.Context, in ToolCallInput) (Verdict, error) {
	return g.engine.Evaluate(in), nil
}

// ---------- 意图基准 ----------

// userIntentText 收集意图基准文本：原始 user prompt + History 中
// role=user 的消息。assistant/tool 消息不计入——被审对象（模型输出）
// 不能自证意图（设计 §8.2 硬规则 1）。
func userIntentText(in ToolCallInput) string {
	var b strings.Builder
	b.WriteString(in.UserPrompt)
	for _, m := range in.History {
		if m.Role != "user" {
			continue
		}
		b.WriteString("\n")
		b.WriteString(m.Content)
	}
	return strings.ToLower(b.String())
}

// deleteIntentKeywords 删除意图关键词（小写）。spike 期用关键词近似
// "会话有删除意图"；语义歧义（指代、否定式）由后续 judge 层负责。
var deleteIntentKeywords = []string{
	// 中文
	"删", "清理", "清除", "移除", "去掉", "丢弃", "干掉", "扔掉", "清掉", "清空",
	// 英文
	"delete", "remove", "drop", "purge", "discard", "get rid of", "clean up", "wipe",
}

// hasDeleteIntent 判断意图基准文本是否表达过删除意图。
func hasDeleteIntent(in ToolCallInput) bool {
	text := userIntentText(in)
	for _, kw := range deleteIntentKeywords {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

// ---------- 规则 1：wiki_delete_page 而会话无删除意图 → deny ----------

func ruleWikiDeleteNoIntent() Rule {
	return Rule{
		ID:          "spike.wiki_delete_requires_intent",
		Description: "删除 wiki 页面前必须能在会话中找到用户的删除意图",
		Match: func(in ToolCallInput) (bool, string) {
			if in.ToolName != spikeToolWikiDeletePage {
				return false, ""
			}
			if hasDeleteIntent(in) {
				return false, ""
			}
			return true, "调用 wiki_delete_page 但原始 prompt 与会话历史的 user 消息中均无删除意图"
		},
	}
}

// ---------- 规则 2：database_query 含 DROP/DELETE 而用户未提及 → deny ----------

// destructiveSQLPatterns 破坏性 SQL 语句模式。要求语句级形态
// （DELETE FROM / DROP TABLE 等），避免 deleted_at 列名、drop_count
// 别名这类词边界内命中。
var destructiveSQLPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bdelete\s+from\b`),
	regexp.MustCompile(`(?i)\bdrop\s+(table|index|view|database|schema|trigger|function|procedure)\b`),
}

// sqlStringLiteralPattern 用于剥离字符串字面量，防止
// `WHERE title LIKE '%delete from%'` 这类数据内容被当成语句。
var sqlStringLiteralPattern = regexp.MustCompile(`'[^']*'|"[^"]*"`)

// destructiveSQLMentionKeywords 用户"提及过破坏性操作"的关键词（小写）。
// 与 deleteIntentKeywords 分开维护：这里的语义是"用户知道会动数据"，
// 泛化的"删"字同样适用，故直接复用删除意图词表。
func userMentionedDestructiveOp(in ToolCallInput) bool {
	return hasDeleteIntent(in)
}

// extractSQLArg 从 database_query 的 args 中取 sql 参数。
// args 缺失或无法解析时返回空串（spike 期不因此拦截）。
func extractSQLArg(args json.RawMessage) string {
	var parsed struct {
		SQL string `json:"sql"`
	}
	if err := json.Unmarshal(args, &parsed); err != nil {
		return ""
	}
	return parsed.SQL
}

func ruleDatabaseQueryDestructiveSQL() Rule {
	return Rule{
		ID:          "spike.database_query_no_unmentioned_destructive_sql",
		Description: "database_query 执行 DROP/DELETE 前必须能在会话中找到用户的对应表述",
		Match: func(in ToolCallInput) (bool, string) {
			if in.ToolName != spikeToolDatabaseQuery {
				return false, ""
			}
			sql := extractSQLArg(in.Args)
			if sql == "" {
				return false, ""
			}
			stmts := sqlStringLiteralPattern.ReplaceAllString(sql, "''")
			matched := ""
			for _, p := range destructiveSQLPatterns {
				if loc := p.FindString(stmts); loc != "" {
					matched = loc
					break
				}
			}
			if matched == "" {
				return false, ""
			}
			if userMentionedDestructiveOp(in) {
				return false, ""
			}
			return true, fmt.Sprintf("SQL 含破坏性语句 %q 但用户未提及删除/销毁操作", matched)
		},
	}
}

// ---------- 规则 3：shell_exec 含 rm -rf 直指家目录 → deny ----------

// extractCommandArg 从 shell_exec 的 args 中取 command 参数。
func extractCommandArg(args json.RawMessage) string {
	var parsed struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &parsed); err != nil {
		return ""
	}
	return parsed.Command
}

// shellSegmentSplitters 把链式命令切成独立段，逐段检查首个 token
// 是否为 rm，避免 "echo rm -rf ~" / "grep 'rm -rf ~' log" 误报。
var shellSegmentSplitters = regexp.MustCompile(`&&|\|\||[;|]`)

// isHomeDirPath 判定路径 token 是否直指家目录本身
// （家目录的子目录不在本 spike 规则范围，见文件头注释）。
func isHomeDirPath(p string) bool {
	p = strings.TrimSuffix(p, "/")
	switch p {
	case "~", "$HOME", "${HOME}", "/root", "/home":
		return true
	}
	// /home/<user>、/Users/<user>（macOS）整目录，不含更深路径。
	for _, prefix := range []string{"/home/", "/Users/"} {
		if rest, ok := strings.CutPrefix(p, prefix); ok && rest != "" && !strings.Contains(rest, "/") {
			return true
		}
	}
	return false
}

// matchRmRfHome 在一条 shell 命令中查找 "rm 带递归+强制 flags 且
// 目标直指家目录" 的形态，命中返回该命令段。
func matchRmRfHome(command string) (segment string, hit bool) {
	for _, seg := range shellSegmentSplitters.Split(command, -1) {
		fields := strings.Fields(seg)
		// 跳过 sudo/doas 提权前缀——提权执行 rm 同样是 rm。
		for len(fields) > 0 && (fields[0] == "sudo" || fields[0] == "doas") {
			fields = fields[1:]
		}
		if len(fields) == 0 || fields[0] != "rm" {
			continue
		}
		hasRecursive, hasForce := false, false
		targets := make([]string, 0, len(fields)-1)
		for _, tok := range fields[1:] {
			if tok == "--" {
				continue
			}
			if strings.HasPrefix(tok, "-") {
				if strings.ContainsAny(tok, "rR") {
					hasRecursive = true
				}
				if strings.Contains(tok, "f") {
					hasForce = true
				}
				continue
			}
			targets = append(targets, tok)
		}
		if !hasRecursive || !hasForce {
			continue
		}
		for _, tgt := range targets {
			if isHomeDirPath(tgt) {
				return strings.TrimSpace(seg), true
			}
		}
	}
	return "", false
}

func ruleShellRmRfHome() Rule {
	return Rule{
		ID:          "spike.shell_no_rm_rf_home",
		Description: "shell 命令不得以 rm -rf 递归强制删除家目录",
		Match: func(in ToolCallInput) (bool, string) {
			if in.ToolName != spikeToolShellExec {
				return false, ""
			}
			command := extractCommandArg(in.Args)
			if command == "" {
				return false, ""
			}
			if seg, hit := matchRmRfHome(command); hit {
				return true, fmt.Sprintf("shell 命令段 %q 以 rm -rf 直指家目录", seg)
			}
			return false, ""
		},
	}
}
