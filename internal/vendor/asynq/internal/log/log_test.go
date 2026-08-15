// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package log

import (
	"bytes"
	"fmt"
	"regexp"
	"testing"
)

// regexp for timestamps
const (
	rgxPID          = `[0-9]+`
	rgxdate         = `[0-9][0-9][0-9][0-9]/[0-9][0-9]/[0-9][0-9]`
	rgxtime         = `[0-9][0-9]:[0-9][0-9]:[0-9][0-9]`
	rgxmicroseconds = `\.[0-9][0-9][0-9][0-9][0-9][0-9]`
)

type tester struct {
	desc        string
	message     string
	wantPattern string // regexp that log output must match
}

func TestLoggerDebug(t *testing.T) {
	tests := []tester{
		{
			desc:    "without trailing newline, logger adds newline",
			message: "hello, world!",
			wantPattern: fmt.Sprintf("^asynq: pid=%s %s %s%s DEBUG: hello, world!\n$",
				rgxPID, rgxdate, rgxtime, rgxmicroseconds),
		},
		{
			desc:    "with trailing newline, logger preserves newline",
			message: "hello, world!\n",
			wantPattern: fmt.Sprintf("^asynq: pid=%s %s %s%s DEBUG: hello, world!\n$",
				rgxPID, rgxdate, rgxtime, rgxmicroseconds),
		},
	}

	for _, tc := range tests {
		var buf bytes.Buffer
		logger := NewLogger(newBase(&buf))

		logger.Debug(tc.message)

		got := buf.String()
		matched, err := regexp.MatchString(tc.wantPattern, got)
		if err != nil {
			t.Fatal("pattern did not compile:", err)
		}
		if !matched {
			t.Errorf("logger.Debug(%q) outputted %q, should match pattern %q",
				tc.message, got, tc.wantPattern)
		}
	}
}

func TestLoggerInfo(t *testing.T) {
	tests := []tester{
		{
			desc:    "without trailing newline, logger adds newline",
			message: "hello, world!",
			wantPattern: fmt.Sprintf("^asynq: pid=%s %s %s%s INFO: hello, world!\n$",
				rgxPID, rgxdate, rgxtime, rgxmicroseconds),
		},
		{
			desc:    "with trailing newline, logger preserves newline",
			message: "hello, world!\n",
			wantPattern: fmt.Sprintf("^asynq: pid=%s %s %s%s INFO: hello, world!\n$",
				rgxPID, rgxdate, rgxtime, rgxmicroseconds),
		},
	}

	for _, tc := range tests {
		var buf bytes.Buffer
		logger := NewLogger(newBase(&buf))

		logger.Info(tc.message)

		got := buf.String()
		matched, err := regexp.MatchString(tc.wantPattern, got)
		if err != nil {
			t.Fatal("pattern did not compile:", err)
		}
		if !matched {
			t.Errorf("logger.Info(%q) outputted %q, should match pattern %q",
				tc.message, got, tc.wantPattern)
		}
	}
}

func TestLoggerWarn(t *testing.T) {
	tests := []tester{
		{
			desc:    "without trailing newline, logger adds newline",
			message: "hello, world!",
			wantPattern: fmt.Sprintf("^asynq: pid=%s %s %s%s WARN: hello, world!\n$",
				rgxPID, rgxdate, rgxtime, rgxmicroseconds),
		},
		{
			desc:    "with trailing newline, logger preserves newline",
			message: "hello, world!\n",
			wantPattern: fmt.Sprintf("^asynq: pid=%s %s %s%s WARN: hello, world!\n$",
				rgxPID, rgxdate, rgxtime, rgxmicroseconds),
		},
	}

	for _, tc := range tests {
		var buf bytes.Buffer
		logger := NewLogger(newBase(&buf))

		logger.Warn(tc.message)

		got := buf.String()
		matched, err := regexp.MatchString(tc.wantPattern, got)
		if err != nil {
			t.Fatal("pattern did not compile:", err)
		}
		if !matched {
			t.Errorf("logger.Warn(%q) outputted %q, should match pattern %q",
				tc.message, got, tc.wantPattern)
		}
	}
}

func TestLoggerError(t *testing.T) {
	tests := []tester{
		{
			desc:    "without trailing newline, logger adds newline",
			message: "hello, world!",
			wantPattern: fmt.Sprintf("^asynq: pid=%s %s %s%s ERROR: hello, world!\n$",
				rgxPID, rgxdate, rgxtime, rgxmicroseconds),
		},
		{
			desc:    "with trailing newline, logger preserves newline",
			message: "hello, world!\n",
			wantPattern: fmt.Sprintf("^asynq: pid=%s %s %s%s ERROR: hello, world!\n$",
				rgxPID, rgxdate, rgxtime, rgxmicroseconds),
		},
	}

	for _, tc := range tests {
		var buf bytes.Buffer
		logger := NewLogger(newBase(&buf))

		logger.Error(tc.message)

		got := buf.String()
		matched, err := regexp.MatchString(tc.wantPattern, got)
		if err != nil {
			t.Fatal("pattern did not compile:", err)
		}
		if !matched {
			t.Errorf("logger.Error(%q) outputted %q, should match pattern %q",
				tc.message, got, tc.wantPattern)
		}
	}
}

type formatTester struct {
	desc        string
	format      string
	args        []interface{}
	wantPattern string // regexp that log output must match
}

func TestLoggerDebugf(t *testing.T) {
	tests := []formatTester{
		{
			desc:   "Formats message with DEBUG prefix",
			format: "hello, %s!",
			args:   []interface{}{"Gopher"},
			wantPattern: fmt.Sprintf("^asynq: pid=%s %s %s%s DEBUG: hello, Gopher!\n$",
				rgxPID, rgxdate, rgxtime, rgxmicroseconds),
		},
	}

	for _, tc := range tests {
		var buf bytes.Buffer
		logger := NewLogger(newBase(&buf))

		logger.Debugf(tc.format, tc.args...)

		got := buf.String()
		matched, err := regexp.MatchString(tc.wantPattern, got)
		if err != nil {
			t.Fatal("pattern did not compile:", err)
		}
		if !matched {
			t.Errorf("logger.Debugf(%q, %v) outputted %q, should match pattern %q",
				tc.format, tc.args, got, tc.wantPattern)
		}
	}
}

func TestLoggerInfof(t *testing.T) {
	tests := []formatTester{
		{
			desc:   "Formats message with INFO prefix",
			format: "%d,%d,%d",
			args:   []interface{}{1, 2, 3},
			wantPattern: fmt.Sprintf("^asynq: pid=%s %s %s%s INFO: 1,2,3\n$",
				rgxPID, rgxdate, rgxtime, rgxmicroseconds),
		},
	}

	for _, tc := range tests {
		var buf bytes.Buffer
		logger := NewLogger(newBase(&buf))

		logger.Infof(tc.format, tc.args...)

		got := buf.String()
		matched, err := regexp.MatchString(tc.wantPattern, got)
		if err != nil {
			t.Fatal("pattern did not compile:", err)
		}
		if !matched {
			t.Errorf("logger.Infof(%q, %v) outputted %q, should match pattern %q",
				tc.format, tc.args, got, tc.wantPattern)
		}
	}
}

func TestLoggerWarnf(t *testing.T) {
	tests := []formatTester{
		{
			desc:   "Formats message with WARN prefix",
			format: "hello, %s",
			args:   []interface{}{"Gophers"},
			wantPattern: fmt.Sprintf("^asynq: pid=%s %s %s%s WARN: hello, Gophers\n$",
				rgxPID, rgxdate, rgxtime, rgxmicroseconds),
		},
	}

	for _, tc := range tests {
		var buf bytes.Buffer
		logger := NewLogger(newBase(&buf))

		logger.Warnf(tc.format, tc.args...)

		got := buf.String()
		matched, err := regexp.MatchString(tc.wantPattern, got)
		if err != nil {
			t.Fatal("pattern did not compile:", err)
		}
		if !matched {
			t.Errorf("logger.Warnf(%q, %v) outputted %q, should match pattern %q",
				tc.format, tc.args, got, tc.wantPattern)
		}
	}
}

func TestLoggerErrorf(t *testing.T) {
	tests := []formatTester{
		{
			desc:   "Formats message with ERROR prefix",
			format: "hello, %s",
			args:   []interface{}{"Gophers"},
			wantPattern: fmt.Sprintf("^asynq: pid=%s %s %s%s ERROR: hello, Gophers\n$",
				rgxPID, rgxdate, rgxtime, rgxmicroseconds),
		},
	}

	for _, tc := range tests {
		var buf bytes.Buffer
		logger := NewLogger(newBase(&buf))

		logger.Errorf(tc.format, tc.args...)

		got := buf.String()
		matched, err := regexp.MatchString(tc.wantPattern, got)
		if err != nil {
			t.Fatal("pattern did not compile:", err)
		}
		if !matched {
			t.Errorf("logger.Errorf(%q, %v) outputted %q, should match pattern %q",
				tc.format, tc.args, got, tc.wantPattern)
		}
	}
}

func TestLoggerWithLowerLevels(t *testing.T) {
	// Logger should not log messages at a level
	// lower than the specified level.
	tests := []struct {
		level Level
		op    string
	}{
		// with level one above
		{InfoLevel, "Debug"},
		{InfoLevel, "Debugf"},
		{WarnLevel, "Info"},
		{WarnLevel, "Infof"},
		{ErrorLevel, "Warn"},
		{ErrorLevel, "Warnf"},
		{FatalLevel, "Error"},
		{FatalLevel, "Errorf"},
		// with skip level
		{WarnLevel, "Debug"},
		{ErrorLevel, "Infof"},
	}

	for _, tc := range tests {
		var buf bytes.Buffer
		logger := NewLogger(newBase(&buf))
		logger.SetLevel(tc.level)

		switch tc.op {
		case "Debug":
			logger.Debug("hello")
		case "Debugf":
			logger.Debugf("hello, %s", "world")
		case "Info":
			logger.Info("hello")
		case "Infof":
			logger.Infof("hello, %s", "world")
		case "Warn":
			logger.Warn("hello")
		case "Warnf":
			logger.Warnf("hello, %s", "world")
		case "Error":
			logger.Error("hello")
		case "Errorf":
			logger.Errorf("hello, %s", "world")
		default:
			t.Fatalf("unexpected op: %q", tc.op)
		}

		if buf.String() != "" {
			t.Errorf("logger.%s outputted log message when level is set to %v", tc.op, tc.level)
		}
	}
}

func TestLoggerWithSameOrHigherLevels(t *testing.T) {
	// Logger should log messages at a level
	// same as or higher than the specified level.
	tests := []struct {
		level Level
		op    string
	}{
		// same level
		{DebugLevel, "Debug"},
		{InfoLevel, "Infof"},
		{WarnLevel, "Warn"},
		{ErrorLevel, "Errorf"},
		// higher level
		{DebugLevel, "Info"},
		{InfoLevel, "Warnf"},
		{WarnLevel, "Error"},
	}

	for _, tc := range tests {
		var buf bytes.Buffer
		logger := NewLogger(newBase(&buf))
		logger.SetLevel(tc.level)

		switch tc.op {
		case "Debug":
			logger.Debug("hello")
		case "Debugf":
			logger.Debugf("hello, %s", "world")
		case "Info":
			logger.Info("hello")
		case "Infof":
			logger.Infof("hello, %s", "world")
		case "Warn":
			logger.Warn("hello")
		case "Warnf":
			logger.Warnf("hello, %s", "world")
		case "Error":
			logger.Error("hello")
		case "Errorf":
			logger.Errorf("hello, %s", "world")
		default:
			t.Fatalf("unexpected op: %q", tc.op)
		}

		if buf.String() == "" {
			t.Errorf("logger.%s did not output log message when level is set to %v", tc.op, tc.level)
		}
	}
}
