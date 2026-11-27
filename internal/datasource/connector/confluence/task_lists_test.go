package confluence

import (
	"strings"
	"testing"
)

func TestConvertPageWithTasksKeepsTaskStates(t *testing.T) {
	// Data Center body.view markup for an inline task list.
	input := `<h2>任务列表</h2><ul class="inline-task-list" data-inline-tasks-content-id="100">` +
		`<li class="checked" data-inline-task-id="1">灰度发布：已完成</li>` +
		`<li data-inline-task-id="2">数据迁移：尚未完成，负责人 <a href="/display/~user">某同事</a></li></ul>` +
		`<ul><li>普通列表项</li></ul>`
	got, err := convertPageWithTasks(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"- [x] 灰度发布：已完成",
		"- [ ] 数据迁移：尚未完成，负责人 [某同事](/display/~user)",
		"- 普通列表项",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in converted page:\n%s", want, got)
		}
	}
	if strings.Contains(got, "WEKNORACONFLUENCETASK") || strings.Contains(got, "- [ ] 普通") {
		t.Errorf("task placeholder leaked or plain list was marked:\n%s", got)
	}
}

func TestConvertPageWithTasksKeepsNestedTaskStates(t *testing.T) {
	input := `<ul class="inline-task-list"><li data-inline-task-id="1">上线` +
		`<ul class="inline-task-list"><li class="checked" data-inline-task-id="2">评审</li></ul>` +
		`<ul><li>备注</li></ul></li></ul>` +
		`<table><tr><td><p>单元格</p><ul class="inline-task-list"><li class="checked">表内任务</li></ul></td></tr></table>`
	got, err := convertPageWithTasks(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"- [ ] 上线", "- [x] 评审", "- 备注", "[x] 表内任务"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in converted page:\n%s", want, got)
		}
	}
	if strings.Contains(got, "WEKNORACONFLUENCETASK") || strings.Contains(got, "[ ] 备注") {
		t.Errorf("task placeholder leaked or plain list was marked:\n%s", got)
	}
}
