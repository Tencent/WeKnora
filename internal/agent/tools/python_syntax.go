package tools

import (
	"fmt"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// pythonQuoteGuidance is the short rule for generated Python. The common
// failure is ASCII quotation marks inside a same-kind string literal
// (`"这不是一个"大干快上"..."`), which Python treats as the end of the
// string. Caught at write/edit time so the model does not burn a round on
// execute_skill_script + py_compile.
const pythonQuoteGuidance = "Python strings: never put ASCII `\"` inside `\"...\"` " +
	"(or `'` inside `'...'`). Use the other quote for the literal, and 「」 " +
	"for Chinese quotation."

var pythonKeywords = map[string]bool{
	"False": true, "None": true, "True": true,
	"and": true, "as": true, "assert": true, "async": true, "await": true,
	"break": true, "class": true, "continue": true, "def": true, "del": true,
	"elif": true, "else": true, "except": true, "finally": true, "for": true,
	"from": true, "global": true, "if": true, "import": true, "in": true,
	"is": true, "lambda": true, "match": true, "nonlocal": true, "not": true,
	"or": true, "pass": true, "raise": true, "return": true, "try": true,
	"while": true, "with": true, "yield": true,
}

// pythonScriptSyntaxHint reports the nested-quote failure, if any, in a file
// just written. editTool names the tool that can repair it: the same guidance
// serves /workspace writes and skill-tree writes, which have different editors.
func pythonScriptSyntaxHint(filePath, src, editTool string) string {
	switch strings.ToLower(path.Ext(filePath)) {
	case ".py":
	default:
		return ""
	}
	line, ok := firstBrokenPythonQuote(src)
	if !ok {
		return ""
	}
	return fmt.Sprintf(
		"Python syntax looks broken around line %d: an ASCII quote inside a "+
			"string of the same kind closed the literal early "+
			"(e.g. (\"这不是一个\"大干快上\"...\")). The file was written. "+
			"Fix it with %s: wrap that text in the other quote, "+
			"or use 「」 / \\\" for the inner quotation. Do not execute the script until it parses.",
		line, editTool,
	)
}

func pythonSyntaxErrorHint(stderr string) string {
	if !strings.Contains(stderr, "SyntaxError") {
		return ""
	}
	return "Hint: this is almost always an ASCII quote inside a same-kind Python string " +
		`(e.g. "这不是一个"大干快上"..."). ` +
		"edit_sandbox_file: wrap the text in the other quote, or replace inner quotes with 「」 / \\\"."
}

// firstBrokenPythonQuote reports the line of the first string literal that
// is immediately followed by a non-keyword identifier — the parse error
// `"这不是一个"大干快上` produces. `"hello" if x` is left alone.
func firstBrokenPythonQuote(src string) (int, bool) {
	line := 1
	for i := 0; i < len(src); {
		switch src[i] {
		case '\n':
			line++
			i++
			continue
		case '#':
			if end := strings.IndexByte(src[i:], '\n'); end < 0 {
				return 0, false
			} else {
				i += end
			}
			continue
		}
		start, quote, triple, fstring, ok := pythonStringStart(src, i)
		if !ok {
			i++
			continue
		}
		end, endLine, unclosed := scanPythonString(src, start, quote, triple, fstring, line)
		if unclosed {
			return line, true
		}
		if !fstring {
			j := end
			for j < len(src) && (src[j] == ' ' || src[j] == '\t' || src[j] == '\r') {
				j++
			}
			if ident, isKw := peekPythonIdent(src, j); ident != "" && !isKw {
				return endLine, true
			}
		}
		i = end
		line = endLine
	}
	return 0, false
}

func pythonStringStart(src string, i int) (contentStart int, quote byte, triple, fstring, ok bool) {
	j := i
	for n := 0; n < 2 && j < len(src) && isPythonStringPrefixByte(src[j]); n++ {
		j++
	}
	if j >= len(src) || (src[j] != '"' && src[j] != '\'') {
		return 0, 0, false, false, false
	}
	if j > i {
		if !validPythonStringPrefix(src[i:j]) {
			return 0, 0, false, false, false
		}
		if i > 0 && isPythonIdentContinueByte(src[i-1]) {
			return 0, 0, false, false, false
		}
	}
	quote = src[j]
	fstring = strings.ContainsAny(src[i:j], "fF")
	if j+2 < len(src) && src[j+1] == quote && src[j+2] == quote {
		return j + 3, quote, true, fstring, true
	}
	return j + 1, quote, false, fstring, true
}

func isPythonStringPrefixByte(b byte) bool {
	switch b {
	case 'r', 'R', 'u', 'U', 'b', 'B', 'f', 'F':
		return true
	default:
		return false
	}
}

func isPythonIdentContinueByte(b byte) bool {
	return b == '_' ||
		(b >= '0' && b <= '9') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= 'a' && b <= 'z')
}

func validPythonStringPrefix(p string) bool {
	switch strings.ToLower(p) {
	case "r", "u", "b", "f", "fr", "rf", "br", "rb":
		return true
	default:
		return false
	}
}

func scanPythonString(src string, i int, quote byte, triple, fstring bool, line int) (end, endLine int, unclosed bool) {
	endLine = line
	brace := 0
	for i < len(src) {
		c := src[i]
		if c == '\\' && i+1 < len(src) {
			if src[i+1] == '\n' {
				endLine++
			}
			i += 2
			continue
		}
		if fstring && brace == 0 && c == '{' {
			if i+1 < len(src) && src[i+1] == '{' {
				i += 2
				continue
			}
			brace++
			i++
			continue
		}
		if fstring && brace == 0 && c == '}' {
			if i+1 < len(src) && src[i+1] == '}' {
				i += 2
				continue
			}
		}
		if fstring && brace > 0 && c == '}' {
			brace--
			i++
			continue
		}
		if fstring && brace > 0 && (c == '"' || c == '\'') {
			nested := i + 1
			tripleNest := i+2 < len(src) && src[i+1] == c && src[i+2] == c
			if tripleNest {
				nested = i + 3
			}
			var unclosedNest bool
			i, endLine, unclosedNest = scanPythonString(src, nested, c, tripleNest, false, endLine)
			if unclosedNest {
				return i, endLine, true
			}
			continue
		}
		if c == '\n' {
			if !triple && brace == 0 {
				return i, endLine, true
			}
			endLine++
			i++
			continue
		}
		if brace == 0 && c == quote {
			if triple {
				if i+2 < len(src) && src[i+1] == quote && src[i+2] == quote {
					return i + 3, endLine, false
				}
				i++
				continue
			}
			return i + 1, endLine, false
		}
		i++
	}
	return i, endLine, true
}

func peekPythonIdent(src string, i int) (string, bool) {
	if i >= len(src) {
		return "", false
	}
	r, size := utf8.DecodeRuneInString(src[i:])
	if !unicode.IsLetter(r) && r != '_' {
		return "", false
	}
	j := i + size
	for j < len(src) {
		r, size = utf8.DecodeRuneInString(src[j:])
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			break
		}
		j += size
	}
	ident := src[i:j]
	return ident, pythonKeywords[ident]
}
