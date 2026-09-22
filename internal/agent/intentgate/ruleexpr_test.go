// 规则编译器（NLC 可编译子集）测试，验收 issue #12（T22）：
//  1. 合法表达式编译执行：`$.amount <= 75` 类参数边界命中/未命中双向断言
//  2. 危险表达式（函数调用、死循环、超长）编译期拒绝
//  3. 执行超时 1ms 熔断返回"不适用"而非 panic
package intentgate

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// ---------- 1. 合法表达式编译 + 参数边界命中/未命中双向断言 ----------

func TestCompileRuleExprValid(t *testing.T) {
	valid := []string{
		`$.amount <= 75`,
		`$.amount > 0 && $.amount <= 75`,
		`$.path == "/etc/passwd"`,
		`$.user.role != "admin"`,
		`!$.force`,
		`$.a + $.b >= 10`,
		`($.a == 1 || $.a == 2) && $.b`,
		`$.items[0].id == "p_1"`,
		`value <= 75`,
		`$ == true`,
		`$.ratio * 2 < 100`,
		`$.amount % 10 == 0`,
	}
	for _, src := range valid {
		if _, err := CompileRuleExpr(src); err != nil {
			t.Errorf("CompileRuleExpr(%q): want ok, got error %v", src, err)
		}
	}
}

func TestRuleExprAmountBoundary(t *testing.T) {
	rule, err := CompileRuleExpr(`$.amount <= 75`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	cases := []struct {
		label string
		args  string
		want  bool
	}{
		{"amount 低于边界", `{"amount": 50}`, true},
		{"amount 恰好等于边界", `{"amount": 75}`, true},
		{"amount 为零", `{"amount": 0}`, true},
		{"amount 为负数", `{"amount": -1}`, true},
		{"amount 略超边界", `{"amount": 75.01}`, false},
		{"amount 远超边界", `{"amount": 100}`, false},
	}
	for _, c := range cases {
		matched, err := rule.Eval(json.RawMessage(c.args))
		if err != nil {
			t.Fatalf("%s: Eval returned error: %v", c.label, err)
		}
		if matched != c.want {
			t.Fatalf("%s: want matched=%v, got %v", c.label, c.want, matched)
		}
	}
}

func TestRuleExprValueAlias(t *testing.T) {
	// arg_path 已在外层抽取参数值时，表达式用 value 指代该值（设计 §6.2）。
	rule, err := CompileRuleExpr(`value <= 75`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	matched, err := rule.Eval(json.RawMessage(`60`))
	if err != nil {
		t.Fatalf("Eval(60): %v", err)
	}
	if !matched {
		t.Fatal("Eval(60): want matched=true")
	}
	matched, err = rule.Eval(json.RawMessage(`80`))
	if err != nil {
		t.Fatalf("Eval(80): %v", err)
	}
	if matched {
		t.Fatal("Eval(80): want matched=false")
	}
}

func TestRuleExprLogicAndStrings(t *testing.T) {
	rule, err := CompileRuleExpr(`$.action == "delete" && !$.confirmed`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	cases := []struct {
		label string
		args  string
		want  bool
	}{
		{"delete 且未确认", `{"action":"delete","confirmed":false}`, true},
		{"delete 且已确认", `{"action":"delete","confirmed":true}`, false},
		{"非 delete", `{"action":"read","confirmed":false}`, false},
	}
	for _, c := range cases {
		matched, err := rule.Eval(json.RawMessage(c.args))
		if err != nil {
			t.Fatalf("%s: Eval returned error: %v", c.label, err)
		}
		if matched != c.want {
			t.Fatalf("%s: want matched=%v, got %v", c.label, c.want, matched)
		}
	}
}

// ---------- 不适用（not applicable）语义 ----------

func TestRuleExprNotApplicable(t *testing.T) {
	rule, err := CompileRuleExpr(`$.amount <= 75`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	cases := []struct {
		label string
		args  string
	}{
		{"参数路径缺失", `{"count": 1}`},
		{"参数类型不匹配（字符串 vs 数值）", `{"amount": "75"}`},
		{"args 不是 JSON", `not-json`},
		{"args 为空", ``},
		{"根路径标量却走对象路径", `42`},
	}
	for _, c := range cases {
		_, err := rule.Eval(json.RawMessage(c.args))
		if !errors.Is(err, ErrRuleNotApplicable) {
			t.Fatalf("%s: want ErrRuleNotApplicable, got %v", c.label, err)
		}
	}

	// 除以零也是不适用，不是 panic。
	divZero, err := CompileRuleExpr(`$.a / $.b > 1`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := divZero.Eval(json.RawMessage(`{"a":1,"b":0}`)); !errors.Is(err, ErrRuleNotApplicable) {
		t.Fatalf("division by zero: want ErrRuleNotApplicable, got %v", err)
	}
	// 顶层结果非布尔（表达式恒为布尔，但根值比较不出结果时同样不适用）。
	cmp, err := CompileRuleExpr(`$.flag == 1`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := cmp.Eval(json.RawMessage(`{"flag": true}`)); !errors.Is(err, ErrRuleNotApplicable) {
		t.Fatalf("bool vs number: want ErrRuleNotApplicable, got %v", err)
	}
}

// ---------- 2. 危险表达式编译期拒绝 ----------

func TestCompileRuleExprRejectDangerous(t *testing.T) {
	dangerous := []struct {
		label string
		src   string
	}{
		{"函数调用 len", `len($.items) > 0`},
		{"函数调用 system", `system("rm -rf /") == 0`},
		{"函数调用伪装在比较右侧", `$.a == eval("1")`},
		{"for 死循环", `for(;;){}`},
		{"while 死循环", `while(true){}`},
		{"未知标识符", `foo == 1`},
		{"空表达式", ``},
		{"纯空白", `   `},
		{"尾部垃圾", `$.a == 1 foo`},
		{"未闭合括号", `($.a == 1`},
		{"未闭合字符串", `$.a == "x`},
		{"赋值语句", `$.a = 1`},
		{"分号拼多语句", `$.a == 1; $.b == 2`},
		{"路径深度超限", `$.` + strings.Repeat("a.", maxRulePathDepth+1) + "z == 1"},
	}
	for _, c := range dangerous {
		if _, err := CompileRuleExpr(c.src); err == nil {
			t.Errorf("%s: CompileRuleExpr(%q) want error, got nil", c.label, c.src)
		}
	}
}

func TestCompileRuleExprRejectTooLong(t *testing.T) {
	// 超长表达式（> maxRuleExprLen 字符）编译期拒绝。
	long := `$.a == 1` + strings.Repeat(` && $.a == 1`, maxRuleExprLen)
	if _, err := CompileRuleExpr(long); err == nil {
		t.Fatalf("expression of %d chars: want error, got nil", len(long))
	}
	// 边界内（恰好等于上限）应能编译。
	fit := `$.a == 1` + strings.Repeat(` && true`, maxRuleExprLen)[:maxRuleExprLen-len(`$.a == 1`)]
	if len(fit) > maxRuleExprLen {
		t.Fatalf("test setup broken: %d > %d", len(fit), maxRuleExprLen)
	}
	if _, err := CompileRuleExpr(fit); err != nil {
		t.Fatalf("expression at max length should compile, got %v", err)
	}
}

func TestCompileRuleExprRejectTooManyNodes(t *testing.T) {
	// 单条不超长的表达式也可能 AST 节点过多（求值成本不可控）。
	// "0-0-0-..."：每段 "-0" 增加 2 个节点，200 段 = 401 节点、401 字符，
	// 节点数超限但长度未超限，确保拒绝来自节点限制而非长度限制。
	deep := "0" + strings.Repeat("-0", 200)
	if len(deep) >= maxRuleExprLen {
		t.Skip("构造的表达式超长，长度限制先行拒绝，跳过")
	}
	if _, err := CompileRuleExpr(deep); err == nil {
		t.Fatal("too many AST nodes: want error, got nil")
	}
}

// ---------- 3. 执行超时 1ms 熔断 ----------

func TestRuleExprTimeout(t *testing.T) {
	rule, err := CompileRuleExpr(`$.amount <= 75`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	// 已过期的预算必须熔断为"不适用"，不 panic、不返回确定性结果。
	matched, err := rule.evalWithDeadline(json.RawMessage(`{"amount": 50}`), time.Now().Add(-time.Second))
	if !errors.Is(err, ErrRuleTimeout) {
		t.Fatalf("expired deadline: want ErrRuleTimeout, got matched=%v err=%v", matched, err)
	}
	if !errors.Is(err, ErrRuleNotApplicable) {
		t.Fatalf("timeout must be a form of not-applicable (errors.Is ErrRuleNotApplicable), got %v", err)
	}
}

func TestRuleExprDefaultBudgetIs1ms(t *testing.T) {
	if ruleEvalBudget != time.Millisecond {
		t.Fatalf("运行期预算必须为 1ms（设计 §8.1），当前 %v", ruleEvalBudget)
	}
	// 常规表达式在 1ms 预算内正常出结果（冒烟，验证预算不是摆设）。
	rule, err := CompileRuleExpr(`$.amount > 0 && $.amount <= 75 && $.currency == "USD"`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	matched, err := rule.Eval(json.RawMessage(`{"amount": 50, "currency": "USD"}`))
	if err != nil {
		t.Fatalf("Eval within budget: %v", err)
	}
	if !matched {
		t.Fatal("want matched=true")
	}
}
