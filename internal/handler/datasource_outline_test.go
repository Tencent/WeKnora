package handler

import (
	"strings"
	"testing"
)

func TestDecodeForceFull(t *testing.T) {
	for _, tc := range []struct {
		body          string
		full, invalid bool
	}{
		{"", false, false}, {"{}", false, false}, {`{"force_full":true}`, true, false},
		{`{"force_full":false}`, false, false}, {`{"force_full":"true"}`, false, true},
		{`{"force_full":1}`, false, true}, {`{"force_full":null}`, false, true},
		{"null", false, true}, {"[]", false, true}, {"{}{}", false, true},
	} {
		t.Run(tc.body, func(t *testing.T) {
			full, err := decodeForceFull(strings.NewReader(tc.body))
			if (err != nil) != tc.invalid || full != tc.full {
				t.Fatalf("full=%v err=%v", full, err)
			}
		})
	}
}
