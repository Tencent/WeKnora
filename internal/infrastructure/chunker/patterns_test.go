package chunker

import "testing"

func TestMarkdownHeadingPattern_BasicLevels(t *testing.T) {
	cases := []struct {
		in    string
		match bool
	}{
		{"# Heading 1", true},
		{"## Heading 2", true},
		{"###### Heading 6", true},
		{"####### Too many", false},
		{"#NoSpace", false},
		{"  # Indented", false},
		{"plain text", false},
	}
	for _, c := range cases {
		got := MarkdownHeadingPattern.MatchString(c.in)
		if got != c.match {
			t.Errorf("MarkdownHeadingPattern(%q): got %v want %v", c.in, got, c.match)
		}
	}
}

func TestNumberedSectionPattern(t *testing.T) {
	cases := []struct {
		in    string
		match bool
	}{
		{"1. Introduction", true},
		{"2.3 Methodology", false}, // requires dot after number
		{"2.3. Methodology", true},
		{"IV. Results", true},
		{"1.Introduction", false}, // requires whitespace
		{"1.", false},              // requires title
		{"plain text", false},
	}
	for _, c := range cases {
		got := NumberedSectionPattern.MatchString(c.in)
		if got != c.match {
			t.Errorf("NumberedSectionPattern(%q): got %v want %v", c.in, got, c.match)
		}
	}
}

func TestGermanChapterPattern(t *testing.T) {
	cases := []struct {
		in    string
		match bool
	}{
		{"Kapitel 1: Einführung", true},
		{"Abschnitt 2.3 Methodik", true},
		{"Abschnitt 3 Methodik", true},
		{"Teil II Ergebnisse", true},
		{"chapter 1", false},
	}
	for _, c := range cases {
		got := GermanChapterPattern.MatchString(c.in)
		if got != c.match {
			t.Errorf("GermanChapterPattern(%q): got %v want %v", c.in, got, c.match)
		}
	}
}

func TestEnglishChapterPattern(t *testing.T) {
	cases := []struct {
		in    string
		match bool
	}{
		{"Chapter 1: Intro", true},
		{"Section 5 Methods", true},
		{"Part IV Results", true},
		{"Kapitel 1", false},
	}
	for _, c := range cases {
		got := EnglishChapterPattern.MatchString(c.in)
		if got != c.match {
			t.Errorf("EnglishChapterPattern(%q): got %v want %v", c.in, got, c.match)
		}
	}
}

func TestChineseChapterPattern(t *testing.T) {
	cases := []struct {
		in    string
		match bool
	}{
		{"第一章 引言", true},
		{"第3节 方法论", true},
		{"第二部分 结果", true},
		{"Chapter 1", false},
	}
	for _, c := range cases {
		got := ChineseChapterPattern.MatchString(c.in)
		if got != c.match {
			t.Errorf("ChineseChapterPattern(%q): got %v want %v", c.in, got, c.match)
		}
	}
}

func TestVisualSeparatorPattern(t *testing.T) {
	cases := []struct {
		in    string
		match bool
	}{
		{"---", true},
		{"========", true},
		{"***", true},
		{"____", true},
		{"--", false}, // needs at least 3
		{"-- text", false},
	}
	for _, c := range cases {
		got := VisualSeparatorPattern.MatchString(c.in)
		if got != c.match {
			t.Errorf("VisualSeparatorPattern(%q): got %v want %v", c.in, got, c.match)
		}
	}
}

func TestPageFooterPattern(t *testing.T) {
	cases := []struct {
		in    string
		match bool
	}{
		{"Seite 3 von 24", true},
		{"Page 5 of 12", true},
		{"page 7", true},
		{"Seite 9", true},
		{"页 3", true},
		{"页码 3 / 12", true},
		{"Some text", false},
	}
	for _, c := range cases {
		got := PageFooterPattern.MatchString(c.in)
		if got != c.match {
			t.Errorf("PageFooterPattern(%q): got %v want %v", c.in, got, c.match)
		}
	}
}

func TestAllCapsHeadingPattern(t *testing.T) {
	cases := []struct {
		in    string
		match bool
	}{
		{"INTRODUCTION", true},
		{"METHODS AND MATERIALS", true},
		{"Mixed Case Heading", false},
		{"ABC", false}, // <4 chars
	}
	for _, c := range cases {
		got := AllCapsHeadingPattern.MatchString(c.in)
		if got != c.match {
			t.Errorf("AllCapsHeadingPattern(%q): got %v want %v", c.in, got, c.match)
		}
	}
}

func TestSentenceSeparators(t *testing.T) {
	if got := SentenceSeparators(LangChinese); got[0] != "。" {
		t.Errorf("Chinese should start with 。, got %v", got)
	}
	if got := SentenceSeparators(LangEnglish); got[0] != ". " {
		t.Errorf("English should start with '. ', got %v", got)
	}
	if got := SentenceSeparators("xx"); len(got) < 5 {
		t.Errorf("Unknown lang should return mixed (>=5 separators), got %v", got)
	}
}

func TestChapterPatternsForLangs(t *testing.T) {
	if got := ChapterPatternsForLangs(nil); len(got) != 3 {
		t.Errorf("nil langs should return all 3, got %d", len(got))
	}
	if got := ChapterPatternsForLangs([]string{LangGerman}); len(got) != 1 {
		t.Errorf("only DE requested should return 1, got %d", len(got))
	}
	if got := ChapterPatternsForLangs([]string{LangGerman, LangChinese}); len(got) != 2 {
		t.Errorf("DE+ZH requested should return 2, got %d", len(got))
	}
	if got := ChapterPatternsForLangs([]string{"xx"}); len(got) != 3 {
		t.Errorf("unknown lang should fall back to all 3, got %d", len(got))
	}
}
