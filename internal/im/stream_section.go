package im

import "strings"

// streamSection tracks newline state for one IM stream text buffer.
type streamSection struct {
	lastCharNewline bool
	text            strings.Builder
}

func (s *streamSection) write(str string) {
	if str == "" {
		return
	}
	s.text.WriteString(str)
	s.lastCharNewline = str[len(str)-1] == '\n'
}

func (s *streamSection) ensureNewlineBefore() {
	if !s.lastCharNewline {
		s.text.WriteByte('\n')
		s.lastCharNewline = true
	}
}
