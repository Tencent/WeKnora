// 规则编译器：NLC 的可编译子集（issue #12 / T22，设计 §6.2、§8.1）。
//
// rule_expr 是一条受限布尔表达式：只能引用工具调用参数（`$.amount`
// 路径，或 arg_path 外层已抽取值时的 `value` 别名）、字面量、比较/
// 逻辑/算术运算符与括号。语法里不存在函数调用与循环，因此危险形态
// 在编译期被结构性拒绝；另有长度/节点数/路径深度三重上限兜底。
//
// 运行期预算 1ms（设计 §8.1）：每个 AST 节点求值前检查预算，超时
// 熔断为"不适用"（ErrRuleTimeout 包裹 ErrRuleNotApplicable），绝不
// panic。"不适用"的判定语义（升级语义层 judge 或按 risk_tier 处置）
// 由 T23 的 Gate 决定，本文件只负责编译与求值。
//
// 自包含实现、不引入表达式语言依赖：子集有意小于任何通用表达式
// 语言（无函数、无成员方法、无管道），依赖面即攻击面。
package intentgate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// 编译期上限。任一超限都在 CompileRuleExpr 阶段拒绝。
const (
	// maxRuleExprLen 表达式源码最大字符数。约束文本场景（金额边界、
	// 路径白名单）几十字符足够，512 已留足余量。
	maxRuleExprLen = 512
	// maxRuleExprNodes AST 节点数上限，限制单条表达式的求值成本。
	maxRuleExprNodes = 256
	// maxRulePathDepth 参数路径最大段数（`$.a.b[0].c` 为 4 段）。
	maxRulePathDepth = 16
	// maxRuleStringLen 字符串字面量最大长度。
	maxRuleStringLen = 256
)

// ruleEvalBudget 运行期求值预算（设计 §8.1：运行超时 1ms）。
const ruleEvalBudget = time.Millisecond

// ErrRuleNotApplicable 表达式对该次调用不适用：参数路径缺失、类型
// 不匹配、除以零、args 无法解析等。适用性失败的处置（升级 judge
// 或按 risk_tier）由上层 Gate 决定。
var ErrRuleNotApplicable = errors.New("intentgate: 规则表达式不适用")

// ErrRuleTimeout 求值超过预算被熔断。它包裹 ErrRuleNotApplicable——
// 超时按"不适用"处置，绝不返回确定性结果。
var ErrRuleTimeout = fmt.Errorf("intentgate: 规则表达式执行超时（预算 %v 熔断）: %w", ruleEvalBudget, ErrRuleNotApplicable)

// CompiledRule 是编译完成的规则表达式，可并发安全地对多次调用求值。
type CompiledRule struct {
	source string
	root   ruleNode
}

// CompileRuleExpr 校验并编译 rule_expr。任何危险形态（函数调用、
// 循环关键字、超长、节点/深度超限、语法残缺）都在此阶段返回 error。
func CompileRuleExpr(src string) (*CompiledRule, error) {
	if len(src) == 0 || len(strings.TrimSpace(src)) == 0 {
		return nil, errors.New("intentgate: rule_expr 为空")
	}
	if len(src) > maxRuleExprLen {
		return nil, fmt.Errorf("intentgate: rule_expr 超长（%d > %d 字符）", len(src), maxRuleExprLen)
	}
	p := &ruleParser{src: src}
	root, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.pos < len(p.src) {
		return nil, fmt.Errorf("intentgate: rule_expr 含无法解析的尾部内容 %q", p.src[p.pos:])
	}
	return &CompiledRule{source: src, root: root}, nil
}

// Source 返回表达式原文（用于日志与 verdict 对账）。
func (r *CompiledRule) Source() string { return r.source }

// Eval 对一次工具调用的 args 求值，预算为 ruleEvalBudget（1ms）。
// 返回 (matched, nil) 表示表达式有确定性结果；返回 ErrRuleNotApplicable
// （含 ErrRuleTimeout）表示本次调用不适用该规则。
func (r *CompiledRule) Eval(args json.RawMessage) (bool, error) {
	return r.evalWithDeadline(args, time.Now().Add(ruleEvalBudget))
}

// evalWithDeadline 与 Eval 同语义，预算由调用方给定。拆出它是
// 超时熔断路径的确定性测试缝（用已过期的 deadline 触发熔断）。
func (r *CompiledRule) evalWithDeadline(args json.RawMessage, deadline time.Time) (bool, error) {
	trimmed := bytes.TrimSpace(args)
	if len(trimmed) == 0 {
		return false, fmt.Errorf("%w: args 为空", ErrRuleNotApplicable)
	}
	var root any
	if err := json.Unmarshal(trimmed, &root); err != nil {
		return false, fmt.Errorf("%w: args 不是合法 JSON: %v", ErrRuleNotApplicable, err)
	}
	env := &ruleEvalEnv{root: root, deadline: deadline}
	v, err := r.root.eval(env)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("%w: 表达式顶层结果不是布尔值", ErrRuleNotApplicable)
	}
	return b, nil
}

// ---------- AST ----------

// ruleEvalEnv 求值环境：root 是 args 解码后的根值，deadline 是预算熔断点。
type ruleEvalEnv struct {
	root     any
	deadline time.Time
}

// checkDeadline 每个节点求值前调用，超时即熔断为"不适用"。
func (e *ruleEvalEnv) checkDeadline() error {
	if time.Now().After(e.deadline) {
		return ErrRuleTimeout
	}
	return nil
}

type ruleNode interface {
	eval(env *ruleEvalEnv) (any, error)
}

// litNode 字面量：float64 / string / bool。
type litNode struct{ val any }

func (n litNode) eval(env *ruleEvalEnv) (any, error) {
	if err := env.checkDeadline(); err != nil {
		return nil, err
	}
	return n.val, nil
}

// pathSeg 参数路径的一段：对象键或数组下标。
type pathSeg struct {
	key   string
	index int
	isIdx bool
}

// pathNode 参数路径（`$.a.b[0]` 或 `value` 根别名）。segs 为空表示根值。
type pathNode struct{ segs []pathSeg }

func (n pathNode) eval(env *ruleEvalEnv) (any, error) {
	if err := env.checkDeadline(); err != nil {
		return nil, err
	}
	cur := env.root
	for _, seg := range n.segs {
		if seg.isIdx {
			arr, ok := cur.([]any)
			if !ok || seg.index < 0 || seg.index >= len(arr) {
				return nil, fmt.Errorf("%w: 数组下标 [%d] 越界或目标不是数组", ErrRuleNotApplicable, seg.index)
			}
			cur = arr[seg.index]
			continue
		}
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: 路径段 %q 的目标不是对象", ErrRuleNotApplicable, seg.key)
		}
		v, ok := obj[seg.key]
		if !ok {
			return nil, fmt.Errorf("%w: 参数路径缺少键 %q", ErrRuleNotApplicable, seg.key)
		}
		cur = v
	}
	return cur, nil
}

// unaryNode 一元运算：`!`（bool）、`-`（number）。
type unaryNode struct {
	op string
	x  ruleNode
}

func (n unaryNode) eval(env *ruleEvalEnv) (any, error) {
	if err := env.checkDeadline(); err != nil {
		return nil, err
	}
	v, err := n.x.eval(env)
	if err != nil {
		return nil, err
	}
	switch n.op {
	case "!":
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("%w: ! 的操作数不是布尔值", ErrRuleNotApplicable)
		}
		return !b, nil
	case "-":
		f, ok := v.(float64)
		if !ok {
			return nil, fmt.Errorf("%w: 一元 - 的操作数不是数值", ErrRuleNotApplicable)
		}
		return -f, nil
	}
	return nil, fmt.Errorf("%w: 未知一元运算符 %q", ErrRuleNotApplicable, n.op)
}

// binaryNode 二元运算：逻辑 && ||、比较 == != < <= > >=、算术 + - * / %。
type binaryNode struct {
	op   string
	l, r ruleNode
}

func (n binaryNode) eval(env *ruleEvalEnv) (any, error) {
	if err := env.checkDeadline(); err != nil {
		return nil, err
	}
	lv, err := n.l.eval(env)
	if err != nil {
		return nil, err
	}
	// 逻辑运算短路：右操作数在左操作数已决定结果时跳过求值。
	switch n.op {
	case "&&":
		lb, ok := lv.(bool)
		if !ok {
			return nil, fmt.Errorf("%w: && 的左操作数不是布尔值", ErrRuleNotApplicable)
		}
		if !lb {
			return false, nil
		}
		return n.evalBoolRight(env, "&&")
	case "||":
		lb, ok := lv.(bool)
		if !ok {
			return nil, fmt.Errorf("%w: || 的左操作数不是布尔值", ErrRuleNotApplicable)
		}
		if lb {
			return true, nil
		}
		return n.evalBoolRight(env, "||")
	}
	rv, err := n.r.eval(env)
	if err != nil {
		return nil, err
	}
	switch n.op {
	case "==", "!=":
		eq, err := ruleValuesEqual(lv, rv)
		if err != nil {
			return nil, err
		}
		if n.op == "!=" {
			return !eq, nil
		}
		return eq, nil
	case "<", "<=", ">", ">=":
		lf, lok := lv.(float64)
		rf, rok := rv.(float64)
		if !lok || !rok {
			return nil, fmt.Errorf("%w: %s 的操作数不都是数值", ErrRuleNotApplicable, n.op)
		}
		switch n.op {
		case "<":
			return lf < rf, nil
		case "<=":
			return lf <= rf, nil
		case ">":
			return lf > rf, nil
		default:
			return lf >= rf, nil
		}
	case "+", "-", "*", "/", "%":
		lf, lok := lv.(float64)
		rf, rok := rv.(float64)
		if !lok || !rok {
			return nil, fmt.Errorf("%w: %s 的操作数不都是数值", ErrRuleNotApplicable, n.op)
		}
		switch n.op {
		case "+":
			return lf + rf, nil
		case "-":
			return lf - rf, nil
		case "*":
			return lf * rf, nil
		case "/":
			if rf == 0 {
				return nil, fmt.Errorf("%w: 除以零", ErrRuleNotApplicable)
			}
			return lf / rf, nil
		default:
			if rf == 0 {
				return nil, fmt.Errorf("%w: 对零取模", ErrRuleNotApplicable)
			}
			return float64(int64(lf) % int64(rf)), nil
		}
	}
	return nil, fmt.Errorf("%w: 未知二元运算符 %q", ErrRuleNotApplicable, n.op)
}

func (n binaryNode) evalBoolRight(env *ruleEvalEnv, op string) (any, error) {
	rv, err := n.r.eval(env)
	if err != nil {
		return nil, err
	}
	rb, ok := rv.(bool)
	if !ok {
		return nil, fmt.Errorf("%w: %s 的右操作数不是布尔值", ErrRuleNotApplicable, op)
	}
	return rb, nil
}

// ruleValuesEqual 同类型相等比较；类型不同即不适用（不存在隐式转换，
// `"75" == 75` 不能静默为真）。
func ruleValuesEqual(l, r any) (bool, error) {
	switch lv := l.(type) {
	case float64:
		rf, ok := r.(float64)
		if !ok {
			return false, fmt.Errorf("%w: 数值与非数值比较", ErrRuleNotApplicable)
		}
		return lv == rf, nil
	case string:
		rs, ok := r.(string)
		if !ok {
			return false, fmt.Errorf("%w: 字符串与非字符串比较", ErrRuleNotApplicable)
		}
		return lv == rs, nil
	case bool:
		rb, ok := r.(bool)
		if !ok {
			return false, fmt.Errorf("%w: 布尔与非布尔比较", ErrRuleNotApplicable)
		}
		return lv == rb, nil
	case nil:
		if r == nil {
			return true, nil
		}
		return false, fmt.Errorf("%w: null 与非 null 比较", ErrRuleNotApplicable)
	}
	return false, fmt.Errorf("%w: 不支持的比较类型 %T", ErrRuleNotApplicable, l)
}

// ---------- 解析器（递归下降，无正则、无反射） ----------

type ruleParser struct {
	src   string
	pos   int
	nodes int
}

func (p *ruleParser) addNode() error {
	p.nodes++
	if p.nodes > maxRuleExprNodes {
		return fmt.Errorf("intentgate: rule_expr 节点数超限（> %d）", maxRuleExprNodes)
	}
	return nil
}

func (p *ruleParser) skipSpace() {
	for p.pos < len(p.src) {
		r, _ := utf8.DecodeRuneInString(p.src[p.pos:])
		if !unicode.IsSpace(r) {
			return
		}
		p.pos += utf8.RuneLen(r)
	}
}

// peekOp 看当前位置是否以给定运算符开头。
func (p *ruleParser) peekOp(op string) bool {
	return strings.HasPrefix(p.src[p.pos:], op)
}

// parseExpr 入口：|| 优先级最低。
func (p *ruleParser) parseExpr() (ruleNode, error) { return p.parseOr() }

func (p *ruleParser) parseOr() (ruleNode, error) {
	l, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for {
		p.skipSpace()
		if !p.peekOp("||") {
			return l, nil
		}
		p.pos += 2
		r, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		if err := p.addNode(); err != nil {
			return nil, err
		}
		l = binaryNode{op: "||", l: l, r: r}
	}
}

func (p *ruleParser) parseAnd() (ruleNode, error) {
	l, err := p.parseCmp()
	if err != nil {
		return nil, err
	}
	for {
		p.skipSpace()
		if !p.peekOp("&&") {
			return l, nil
		}
		p.pos += 2
		r, err := p.parseCmp()
		if err != nil {
			return nil, err
		}
		if err := p.addNode(); err != nil {
			return nil, err
		}
		l = binaryNode{op: "&&", l: l, r: r}
	}
}

// parseCmp 比较运算不可链式（`a < b < c` 拒绝，避免直觉语义陷阱）。
func (p *ruleParser) parseCmp() (ruleNode, error) {
	l, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	op := ""
	for _, candidate := range []string{"==", "!=", "<=", ">=", "<", ">"} {
		if p.peekOp(candidate) {
			op = candidate
			break
		}
	}
	if op == "" {
		return l, nil
	}
	p.pos += len(op)
	r, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	if err := p.addNode(); err != nil {
		return nil, err
	}
	return binaryNode{op: op, l: l, r: r}, nil
}

func (p *ruleParser) parseAdd() (ruleNode, error) {
	l, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for {
		p.skipSpace()
		op := ""
		switch {
		case p.peekOp("+"):
			op = "+"
		case p.peekOp("-"):
			op = "-"
		default:
			return l, nil
		}
		p.pos++
		r, err := p.parseMul()
		if err != nil {
			return nil, err
		}
		if err := p.addNode(); err != nil {
			return nil, err
		}
		l = binaryNode{op: op, l: l, r: r}
	}
}

func (p *ruleParser) parseMul() (ruleNode, error) {
	l, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		p.skipSpace()
		op := ""
		switch {
		case p.peekOp("*"):
			op = "*"
		case p.peekOp("/"):
			op = "/"
		case p.peekOp("%"):
			op = "%"
		default:
			return l, nil
		}
		p.pos++
		r, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		if err := p.addNode(); err != nil {
			return nil, err
		}
		l = binaryNode{op: op, l: l, r: r}
	}
}

func (p *ruleParser) parseUnary() (ruleNode, error) {
	p.skipSpace()
	if p.peekOp("!") && !p.peekOp("!=") {
		p.pos++
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		if err := p.addNode(); err != nil {
			return nil, err
		}
		return unaryNode{op: "!", x: x}, nil
	}
	if p.peekOp("-") {
		p.pos++
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		if err := p.addNode(); err != nil {
			return nil, err
		}
		return unaryNode{op: "-", x: x}, nil
	}
	return p.parsePrimary()
}

// parsePrimary 字面量、参数路径、括号。语法里没有函数调用与标识符
// 变量（value 除外），任何其他标识符在此被拒绝。
func (p *ruleParser) parsePrimary() (ruleNode, error) {
	p.skipSpace()
	if p.pos >= len(p.src) {
		return nil, errors.New("intentgate: rule_expr 意外结束（缺少操作数）")
	}
	c := p.src[p.pos]
	switch {
	case c == '(':
		p.pos++
		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if p.pos >= len(p.src) || p.src[p.pos] != ')' {
			return nil, errors.New("intentgate: rule_expr 括号未闭合")
		}
		p.pos++
		return inner, nil
	case c == '$':
		p.pos++
		return p.parsePath()
	case c == '"':
		s, err := p.parseString()
		if err != nil {
			return nil, err
		}
		if err := p.addNode(); err != nil {
			return nil, err
		}
		return litNode{val: s}, nil
	case c >= '0' && c <= '9':
		f, err := p.parseNumber()
		if err != nil {
			return nil, err
		}
		if err := p.addNode(); err != nil {
			return nil, err
		}
		return litNode{val: f}, nil
	case c == '_' || unicode.IsLetter(rune(c)):
		ident := p.parseIdent()
		switch ident {
		case "true":
			if err := p.addNode(); err != nil {
				return nil, err
			}
			return litNode{val: true}, nil
		case "false":
			if err := p.addNode(); err != nil {
				return nil, err
			}
			return litNode{val: false}, nil
		case "value":
			// arg_path 外层已抽取参数值时，value 指代根值（设计 §6.2）。
			if err := p.addNode(); err != nil {
				return nil, err
			}
			return pathNode{}, nil
		}
		p.skipSpace()
		if p.pos < len(p.src) && p.src[p.pos] == '(' {
			return nil, fmt.Errorf("intentgate: rule_expr 不允许函数调用 %q", ident)
		}
		return nil, fmt.Errorf("intentgate: rule_expr 不允许标识符 %q（只支持 $ 路径与 value）", ident)
	default:
		return nil, fmt.Errorf("intentgate: rule_expr 无法解析的字符 %q", string(c))
	}
}

// parsePath 解析 `$` 之后的路径段：`.key` 或 `[index]` / `["key"]`。
func (p *ruleParser) parsePath() (ruleNode, error) {
	var segs []pathSeg
	for {
		if p.pos < len(p.src) && p.src[p.pos] == '.' {
			p.pos++
			if p.pos >= len(p.src) || !isIdentStart(p.src[p.pos]) {
				return nil, errors.New("intentgate: rule_expr 路径 . 后缺少键名")
			}
			segs = append(segs, pathSeg{key: p.parseIdent()})
		} else if p.pos < len(p.src) && p.src[p.pos] == '[' {
			p.pos++
			p.skipSpace()
			if p.pos < len(p.src) && p.src[p.pos] == '"' {
				key, err := p.parseString()
				if err != nil {
					return nil, err
				}
				segs = append(segs, pathSeg{key: key})
			} else {
				start := p.pos
				for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
					p.pos++
				}
				if p.pos == start {
					return nil, errors.New("intentgate: rule_expr 数组下标缺失")
				}
				idx, err := strconv.Atoi(p.src[start:p.pos])
				if err != nil {
					return nil, fmt.Errorf("intentgate: rule_expr 数组下标非法: %v", err)
				}
				segs = append(segs, pathSeg{index: idx, isIdx: true})
			}
			p.skipSpace()
			if p.pos >= len(p.src) || p.src[p.pos] != ']' {
				return nil, errors.New("intentgate: rule_expr 数组下标 ] 未闭合")
			}
			p.pos++
		} else {
			break
		}
		if len(segs) > maxRulePathDepth {
			return nil, fmt.Errorf("intentgate: rule_expr 路径深度超限（> %d 段）", maxRulePathDepth)
		}
	}
	if err := p.addNode(); err != nil {
		return nil, err
	}
	return pathNode{segs: segs}, nil
}

// parseNumber 解析非负数值字面量（负号由一元 - 处理）。
func (p *ruleParser) parseNumber() (float64, error) {
	start := p.pos
	for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
		p.pos++
	}
	if p.pos < len(p.src) && p.src[p.pos] == '.' {
		p.pos++
		if p.pos >= len(p.src) || p.src[p.pos] < '0' || p.src[p.pos] > '9' {
			return 0, errors.New("intentgate: rule_expr 数值小数点后缺少数字")
		}
		for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
			p.pos++
		}
	}
	f, err := strconv.ParseFloat(p.src[start:p.pos], 64)
	if err != nil {
		return 0, fmt.Errorf("intentgate: rule_expr 数值字面量非法: %v", err)
	}
	return f, nil
}

// parseString 解析双引号字符串，支持 \" \\ \n \t \r 转义。
func (p *ruleParser) parseString() (string, error) {
	p.pos++ // 跳过开头的 "
	var b strings.Builder
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch c {
		case '"':
			p.pos++
			if b.Len() > maxRuleStringLen {
				return "", fmt.Errorf("intentgate: rule_expr 字符串字面量超长（> %d）", maxRuleStringLen)
			}
			return b.String(), nil
		case '\\':
			p.pos++
			if p.pos >= len(p.src) {
				return "", errors.New("intentgate: rule_expr 字符串转义未闭合")
			}
			switch esc := p.src[p.pos]; esc {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '"', '\\':
				b.WriteByte(esc)
			default:
				return "", fmt.Errorf("intentgate: rule_expr 不支持的转义 \\%c", esc)
			}
			p.pos++
		default:
			b.WriteByte(c)
			p.pos++
		}
	}
	return "", errors.New("intentgate: rule_expr 字符串未闭合")
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func (p *ruleParser) parseIdent() string {
	start := p.pos
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			p.pos++
			continue
		}
		break
	}
	return p.src[start:p.pos]
}
