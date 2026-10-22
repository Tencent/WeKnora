package youtube

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseJSON3SkipsAppendEvents(t *testing.T) {
	data := []byte(`{"events":[
		{"tStartMs":0,"dDurationMs":835199,"id":1},
		{"tStartMs":1040,"segs":[{"utf8":"hello"},{"utf8":" welcome","tOffsetMs":440}]},
		{"tStartMs":2990,"aAppend":1,"segs":[{"utf8":"\n"}]},
		{"tStartMs":3000,"segs":[{"utf8":"to the\nchallenge &amp; more"}]},
		{"tStartMs":4000,"segs":[{"utf8":"  "}]}
	]}`)
	got, err := ParseJSON3(data)
	if err != nil {
		t.Fatal(err)
	}
	want := []Segment{{Start: 1.04, Text: "hello welcome"}, {Start: 3, Text: "to the challenge & more"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseJSON3 = %+v, want %+v", got, want)
	}
}

func TestParseJSON3RejectsInvalid(t *testing.T) {
	if _, err := ParseJSON3([]byte("not json")); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseVTTDeduplicatesRollingCaptions(t *testing.T) {
	data := []byte(`WEBVTT
Kind: captions
Language: en

00:00:01.040 --> 00:00:02.990 align:start position:0%
hello<00:00:01.480><c> welcome</c>

00:00:02.990 --> 00:00:03.000 align:start position:0%
hello welcome

00:00:03.000 --> 00:00:04.870 align:start position:0%
hello welcome
to<00:00:03.520><c> the challenge</c>

01:02:03.500 --> 01:02:05.000
<b>later</b> line
`)
	got, err := ParseVTT(data)
	if err != nil {
		t.Fatal(err)
	}
	want := []Segment{
		{Start: 1.04, Text: "hello welcome"},
		{Start: 3, Text: "to the challenge"},
		{Start: 3723.5, Text: "later line"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseVTT = %+v, want %+v", got, want)
	}
}

func TestFormatTranscriptGroupsByTime(t *testing.T) {
	segments := []Segment{
		{Start: 0, Text: "one"},
		{Start: 30, Text: "two"},
		{Start: 61, Text: "three"},
		{Start: 3700, Text: "four"},
	}
	got := FormatTranscript(segments, 60)
	want := "[0:00] one two\n\n[1:01] three\n\n[1:01:40] four"
	if got != want {
		t.Fatalf("FormatTranscript =\n%s\nwant\n%s", got, want)
	}
	if FormatTranscript(nil, 60) != "" {
		t.Fatal("empty segments should render empty transcript")
	}
}

func TestFormatTimestamp(t *testing.T) {
	for seconds, want := range map[float64]string{-5: "0:00", 9.9: "0:09", 754: "12:34", 3600: "1:00:00"} {
		if got := FormatTimestamp(seconds); got != want {
			t.Errorf("FormatTimestamp(%v) = %q, want %q", seconds, got, want)
		}
	}
}

func TestSplitText(t *testing.T) {
	text := strings.Join([]string{"aaaa", "bbbb", "cccc"}, "\n\n")
	if got := SplitText(text, 100); !reflect.DeepEqual(got, []string{text}) {
		t.Fatalf("short text should stay whole, got %q", got)
	}
	if got := SplitText(text, 10); !reflect.DeepEqual(got, []string{"aaaa\n\nbbbb", "cccc"}) {
		t.Fatalf("SplitText by paragraph = %q", got)
	}
	if got := SplitText("abcdefghij", 4); !reflect.DeepEqual(got, []string{"abcd", "efgh", "ij"}) {
		t.Fatalf("SplitText hard split = %q", got)
	}
	if got := SplitText("   ", 4); got != nil {
		t.Fatalf("blank text should produce no pieces, got %q", got)
	}
}
