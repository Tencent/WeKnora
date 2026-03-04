package chatpipline

import (
	"testing"
)

func TestCleanPassageForRerank(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "plain text unchanged",
			input:  "这是一段普通的文本内容",
			expect: "这是一段普通的文本内容",
		},
		{
			name:   "remove markdown images",
			input:  "前文 ![图片说明](https://example.com/img.png) 后文",
			expect: "前文  后文",
		},
		{
			name:   "convert markdown links to text",
			input:  "请参考 [官方文档](https://docs.example.com) 了解详情",
			expect: "请参考 官方文档 了解详情",
		},
		{
			name:   "remove standalone URLs",
			input:  "访问 https://example.com/path?q=1&b=2 获取更多信息",
			expect: "访问  获取更多信息",
		},
		{
			name:   "remove code blocks",
			input:  "示例代码：\n```python\nprint('hello')\n```\n以上是示例",
			expect: "示例代码：\n\n以上是示例",
		},
		{
			name:   "remove LaTeX blocks",
			input:  "公式如下 $$E=mc^2$$ 其中E是能量",
			expect: "公式如下  其中E是能量",
		},
		{
			name:   "remove table separator rows",
			input:  "| 名称 | 值 |\n| --- | --- |\n| A | 1 |",
			expect: "| 名称 | 值 |\n\n| A | 1 |",
		},
		{
			name:   "strip heading markers",
			input:  "## 第二章 概述\n### 2.1 背景",
			expect: "第二章 概述\n2.1 背景",
		},
		{
			name:   "strip blockquote markers",
			input:  "> 这是一段引用\n> 第二行引用",
			expect: "这是一段引用\n第二行引用",
		},
		{
			name:   "unwrap bold and italic",
			input:  "这是 **加粗** 和 *斜体* 以及 ***粗斜体*** 文本",
			expect: "这是 加粗 和 斜体 以及 粗斜体 文本",
		},
		{
			name:   "strip list markers",
			input:  "- 项目一\n- 项目二\n1. 有序一\n2. 有序二",
			expect: "项目一\n项目二\n有序一\n有序二",
		},
		{
			name:   "remove HTML tags",
			input:  "文本<br>换行<div class=\"test\">内容</div>结尾",
			expect: "文本换行内容结尾",
		},
		{
			name:   "collapse excessive newlines",
			input:  "段落一\n\n\n\n\n段落二",
			expect: "段落一\n\n段落二",
		},
		{
			name: "combined real-world passage",
			input: `## 产品介绍

这是一个 **重要的** 产品。详见 [产品页面](https://example.com/product)。

![产品截图](images/product.png)

> 用户评价：非常好用

- 功能一
- 功能二

` + "```json\n{\"key\": \"value\"}\n```",
			expect: "产品介绍\n\n这是一个 重要的 产品。详见 产品页面。\n\n用户评价：非常好用\n\n功能一\n功能二",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanPassageForRerank(tt.input)
			if got != tt.expect {
				t.Errorf("cleanPassageForRerank():\ngot:    %q\nexpect: %q", got, tt.expect)
			}
		})
	}
}
