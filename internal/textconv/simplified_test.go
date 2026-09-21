package textconv

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestToSimplified(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"", ""},
		{"請聯繫客服，開發環境", "请联系客服，开发环境"},
		{"乾隆年間乾乾淨淨的頭髮", "乾隆年间干干净净的头发"},
		{"一目瞭然，不瞭解？", "一目了然，不了解？"},
		{"😀臺灣ABC𠮷\x00後", "😀台湾ABC𠮷\x00后"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			if got := ToSimplified(tc.input); got != tc.want {
				t.Errorf("ToSimplified(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// Captured from longbridgeapp/opencc v0.3.13's t2s conversion before removing
// that dependency. Every dictionary key is exercised alone, in context and
// repeated. Changing this digest requires reviewing FAQ hash compatibility.
func TestHistoricalConversionCorpus(t *testing.T) {
	h := sha256.New()
	count := 0
	check := func(input string) {
		if _, err := fmt.Fprintf(h, "%s\x00%s\x00", input, ToSimplified(input)); err != nil {
			t.Fatal(err)
		}
		count++
	}
	for _, data := range []string{phrasesText, charactersText} {
		for _, line := range strings.Split(strings.TrimSuffix(data, "\n"), "\n") {
			key, _, _ := strings.Cut(line, "\t")
			for _, input := range []string{key, "前" + key + "後", key + key} {
				check(input)
			}
		}
	}
	for _, input := range []string{"乾隆年間乾乾淨淨的頭髮", "一目瞭然，不瞭解？", "請聯繫客服，開發環境", "😀臺灣ABC𠮷\x00後", "乾坤大挪移", "著作權與軟體開發", ""} {
		check(input)
	}
	const want = "f3afa240ee7460932bbef67207d23302c5d9722b70c1f706ce071d2f35bfbcc0"
	if got := fmt.Sprintf("%x", h.Sum(nil)); got != want || count != 13177 {
		t.Fatalf("historical conversion changed: count=%d digest=%s, want 13177/%s", count, got, want)
	}
}

func TestDictionaryPrecedenceAndAlternatives(t *testing.T) {
	c := newConverter("甲乙\t首選 次選\n甲乙丙\t最長\n", "甲乙丙丁\t不應優先\n丁\t尾\n")
	if got := c.convert("甲乙丙丁甲乙"); got != "最長尾首選" {
		t.Fatalf("phrase precedence/longest match/first alternative changed: %q", got)
	}
}
