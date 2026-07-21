// Package assets embeds static binary fixtures used in tests and tooling.
package assets

import _ "embed"

// ASRTestWAV is a short WAV sample used by ASR unit tests.
//
//go:embed asr_test.wav
var ASRTestWAV []byte
