package transcript

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestBuildFromJSONMapsMPSResultEnvelope(t *testing.T) {
	doc, err := BuildFromJSON(RawInput{
		VideoID: "video-1", TranscriptGeneration: "mps:task-1", Title: "课程标题",
		DurationSeconds: 12, Provider: "tencent_mps",
		Payload: []byte(`{"task_id":"task-1","mps_result":{"segments":[{"source_segment_id":"seg-1","text":"你好","start_ms":100,"end_ms":900,"speaker_id":"speaker-1"},{"source_segment_id":"seg-2","text":"世界","start_ms":1000,"end_ms":1800,"speaker_id":"speaker-2"}]}}`),
	})
	if err != nil {
		t.Fatalf("BuildFromJSON returned error: %v", err)
	}
	if len(doc.Chapters) != 1 || len(doc.Chapters[0].Paragraphs) != 2 {
		t.Fatalf("unexpected document shape: %+v", doc)
	}
	first := doc.Chapters[0].Paragraphs[0].TimeMarks[0]
	if first.SourceSentenceID != "seg-1" || first.EvidenceSentenceID == "" || first.StartMs != 100 || first.EndMs != 900 {
		t.Fatalf("source mapping was not preserved: %+v", first)
	}
	if doc.ContinuousText != "你好世界" {
		t.Fatalf("continuous text = %q", doc.ContinuousText)
	}
}

func TestBuildFromJSONMapsRawMPSSegmentSecondsAndSpeaker(t *testing.T) {
	doc, err := BuildFromJSON(RawInput{
		VideoID: "video-mps-raw", TranscriptGeneration: "mps:raw-task", Title: "原始 MPS 样本", DurationSeconds: 8,
		Provider: "mps",
		Payload:  []byte(`{"engine":"tencent-mps","segments":[{"start_sec":0.165,"end_sec":4.565,"speaker":"Zara Zhang","text":"最近我在日本的一个书店发现他们卖很多书，"},{"start_sec":4.715,"end_sec":5.94,"speaker":"Zara Zhang","text":"我觉得非常神奇。"}]}`),
	})
	if err != nil {
		t.Fatalf("BuildFromJSON returned error: %v", err)
	}
	paragraph := doc.Chapters[0].Paragraphs[0]
	if paragraph.SpeakerID != "Zara Zhang" || paragraph.StartMs != 165 || paragraph.EndMs != 5940 {
		t.Fatalf("raw MPS speaker/timing was not mapped: %+v", paragraph)
	}
	if len(doc.Chapters[0].Paragraphs) != 1 || len(paragraph.TimeMarks) != 2 {
		t.Fatalf("short same-speaker subtitles were not merged with source marks retained: %+v", doc.Chapters[0].Paragraphs)
	}
	wantMarks := []struct {
		id, text   string
		start, end int
	}{{"mps-segment-000001", "最近我在日本的一个书店发现他们卖很多书，", 165, 4565}, {"mps-segment-000002", "我觉得非常神奇。", 4715, 5940}}
	for index, want := range wantMarks {
		mark := paragraph.TimeMarks[index]
		if mark.SourceSentenceID != want.id || mark.Text != want.text || mark.StartMs != want.start || mark.EndMs != want.end {
			t.Fatalf("time mark %d changed during merge: %+v", index, mark)
		}
	}
	if paragraph.TimeMarks[0].SourceSentenceID == "" || paragraph.TimeMarks[0].StartMs != 165 || paragraph.TimeMarks[0].EndMs != 4565 {
		t.Fatalf("raw MPS sentence mapping was not generated: %+v", paragraph.TimeMarks[0])
	}
}

func TestBuildFromJSONShortSubtitleMergeBoundaries(t *testing.T) {
	input := RawInput{
		VideoID: "video-merge", TranscriptGeneration: "generation-merge", Title: "短字幕合并", DurationSeconds: 20,
		Provider: "mps",
		Payload: []byte(`{"segments":[
			{"source_segment_id":"a","text":"这是一个未完的","start_ms":0,"end_ms":500,"speaker":"spk-1"},
			{"source_segment_id":"b","text":"连续话语。","start_ms":700,"end_ms":1200,"speaker":"spk-1"},
			{"source_segment_id":"c","text":"新说话人短句","start_ms":1300,"end_ms":1600,"speaker":"spk-2"},
			{"source_segment_id":"d","text":"不应跨人合并","start_ms":1700,"end_ms":2200,"speaker":"spk-1"},
			{"source_segment_id":"e","text":"间隔太长","start_ms":4000,"end_ms":4500,"speaker":"spk-1"},
			{"source_segment_id":"f","text":"句号后的句子","start_ms":4600,"end_ms":5200,"speaker":"spk-1"}
		]}`),
	}
	doc, err := BuildFromJSON(input)
	if err != nil {
		t.Fatalf("BuildFromJSON returned error: %v", err)
	}
	paragraphs := doc.Chapters[0].Paragraphs
	if len(paragraphs) != 4 {
		t.Fatalf("paragraph count = %d, want 4: %+v", len(paragraphs), paragraphs)
	}
	if len(paragraphs[0].TimeMarks) != 2 || paragraphs[0].Text != "这是一个未完的连续话语。" {
		t.Fatalf("first short utterance was not merged: %+v", paragraphs[0])
	}
	want := []struct {
		id, text   string
		start, end int
	}{{"a", "这是一个未完的", 0, 500}, {"b", "连续话语。", 700, 1200}, {"c", "新说话人短句", 1300, 1600}, {"d", "不应跨人合并", 1700, 2200}, {"e", "间隔太长", 4000, 4500}, {"f", "句号后的句子", 4600, 5200}}
	markIndex := 0
	for paragraphIndex, paragraph := range paragraphs {
		for _, mark := range paragraph.TimeMarks {
			if mark.SourceSentenceID != want[markIndex].id || mark.Text != want[markIndex].text || mark.StartMs != want[markIndex].start || mark.EndMs != want[markIndex].end || mark.EvidenceSentenceID == "" {
				t.Fatalf("paragraph %d changed source mapping at mark %d: %+v", paragraphIndex, markIndex, mark)
			}
			markIndex++
		}
	}
}

func TestBuildFromJSONDoesNotMergeAfterTerminalPunctuation(t *testing.T) {
	doc, err := BuildFromJSON(RawInput{
		VideoID: "video-terminal", TranscriptGeneration: "generation-terminal", Title: "完整句边界", DurationSeconds: 4,
		Provider: "mps",
		Payload:  []byte(`{"segments":[{"source_segment_id":"a","text":"这是完整句。","start_ms":0,"end_ms":500,"speaker":"spk"},{"source_segment_id":"b","text":"这是下一句","start_ms":600,"end_ms":1100,"speaker":"spk"}]}`),
	})
	if err != nil {
		t.Fatalf("BuildFromJSON returned error: %v", err)
	}
	if got := len(doc.Chapters[0].Paragraphs); got != 2 {
		t.Fatalf("paragraph count = %d, want 2 after terminal punctuation", got)
	}
}

func TestBuildFromJSONMergeRequiresKnownSpeakerAndAllowsMaxGap(t *testing.T) {
	for _, testCase := range []struct {
		name, speaker  string
		wantParagraphs int
	}{
		{"exact gap", "spk", 1},
		{"unknown speaker", "", 2},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			payload := fmt.Sprintf(`{"segments":[{"source_segment_id":"a","text":"前半，","start_ms":0,"end_ms":500,"speaker":%q},{"source_segment_id":"b","text":"后半","start_ms":1700,"end_ms":2200,"speaker":%q}]}`, testCase.speaker, testCase.speaker)
			doc, err := BuildFromJSON(RawInput{VideoID: "video-gap", TranscriptGeneration: "generation-gap", Title: "间隔", DurationSeconds: 4, Provider: "mps", Payload: []byte(payload)})
			if err != nil {
				t.Fatalf("BuildFromJSON returned error: %v", err)
			}
			if got := len(doc.Chapters[0].Paragraphs); got != testCase.wantParagraphs {
				t.Fatalf("paragraph count = %d, want %d", got, testCase.wantParagraphs)
			}
		})
	}
}

func TestBuildFromJSONTreatsEllipsisAsTerminalBoundary(t *testing.T) {
	doc, err := BuildFromJSON(RawInput{
		VideoID: "video-ellipsis", TranscriptGeneration: "generation-ellipsis", Title: "省略号", DurationSeconds: 4, Provider: "mps",
		Payload: []byte(`{"segments":[{"source_segment_id":"a","text":"语气结束…","start_ms":0,"end_ms":500,"speaker":"spk"},{"source_segment_id":"b","text":"下一句","start_ms":600,"end_ms":1000,"speaker":"spk"}]}`),
	})
	if err != nil {
		t.Fatalf("BuildFromJSON returned error: %v", err)
	}
	if got := len(doc.Chapters[0].Paragraphs); got != 2 {
		t.Fatalf("paragraph count = %d, want 2 after ellipsis", got)
	}
}

func TestBuildFromJSONMergeConfigCanDisableMerge(t *testing.T) {
	cases := []struct {
		name   string
		config MergeConfig
	}{
		{"gap", MergeConfig{MaxGapMs: 1}},
		{"short", MergeConfig{ShortMaxRunes: 1}},
		{"unit length", MergeConfig{MaxUnitRunes: 3}},
		{"unit duration", MergeConfig{MaxUnitDurationMs: 600}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			doc, err := BuildFromJSON(RawInput{
				VideoID: "video-no-merge", TranscriptGeneration: "generation-no-merge", Title: "不合并", DurationSeconds: 3,
				Provider: "mps", Merge: testCase.config,
				Payload: []byte(`{"segments":[{"source_segment_id":"a","text":"未完","start_ms":0,"end_ms":500,"speaker":"spk"},{"source_segment_id":"b","text":"续句","start_ms":100,"end_ms":700,"speaker":"spk"}]}`),
			})
			if err != nil {
				t.Fatalf("BuildFromJSON returned error: %v", err)
			}
			if got := len(doc.Chapters[0].Paragraphs); got != 2 {
				t.Fatalf("paragraph count = %d, want 2 with tightened threshold", got)
			}
		})
	}
}

func TestBuildFromJSONMergeThresholdsAreInclusive(t *testing.T) {
	for _, testCase := range []struct {
		name                             string
		firstText, secondText            string
		firstEnd, secondStart, secondEnd int
		wantParagraphs                   int
	}{
		{"max length", strings.Repeat("a", 158) + ",", "b", 1000, 1100, 2000, 1},
		{"length over limit", strings.Repeat("a", 159) + ",", "b", 1000, 1100, 2000, 2},
		{"max duration", "上一段，", "续", 14000, 14050, 15000, 1},
		{"duration over limit", "上一段，", "续", 14000, 14050, 15001, 2},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			payload := fmt.Sprintf(`{"segments":[{"source_segment_id":"a","text":%q,"start_ms":0,"end_ms":%d,"speaker":"spk"},{"source_segment_id":"b","text":%q,"start_ms":%d,"end_ms":%d,"speaker":"spk"}]}`, testCase.firstText, testCase.firstEnd, testCase.secondText, testCase.secondStart, testCase.secondEnd)
			doc, err := BuildFromJSON(RawInput{VideoID: "video-threshold", TranscriptGeneration: "generation-threshold", Title: "阈值", DurationSeconds: 20, Provider: "mps", Payload: []byte(payload)})
			if err != nil {
				t.Fatalf("BuildFromJSON returned error: %v", err)
			}
			if got := len(doc.Chapters[0].Paragraphs); got != testCase.wantParagraphs {
				t.Fatalf("paragraph count = %d, want %d", got, testCase.wantParagraphs)
			}
		})
	}
}

func TestBuildFromJSONMapsDownloadedTingwuWords(t *testing.T) {
	payload := []byte(`{"Transcription":{"Paragraphs":[{"ParagraphId":"p-1","SpeakerId":"spk-1","Words":[{"SentenceId":1,"Start":100,"End":300,"Text":"你好"},{"SentenceId":1,"Start":300,"End":500,"Text":"世界"},{"SentenceId":2,"Start":600,"End":800,"Text":"再见"}]},{"ParagraphId":"p-2","SpeakerId":"spk-2","Words":[{"SentenceId":1,"Start":900,"End":1200,"Text":"继续"}]}]}}`)
	doc, err := BuildFromJSON(RawInput{VideoID: "video-2", TranscriptGeneration: "generation-2", Title: "访谈", DurationSeconds: 3, Provider: "aliyun_tingwu", Payload: payload})
	if err != nil {
		t.Fatalf("BuildFromJSON returned error: %v", err)
	}
	marks := make([]TimeMark, 0, 3)
	for _, paragraph := range doc.Chapters[0].Paragraphs {
		marks = append(marks, paragraph.TimeMarks...)
	}
	if len(marks) != 3 {
		t.Fatalf("time mark count = %d", len(marks))
	}
	if marks[0].SourceSentenceID != "1" || marks[1].SourceSentenceID != "2" || marks[2].SourceSentenceID != "p-2:1:2" {
		t.Fatalf("duplicate provider IDs were not qualified deterministically: %+v", marks)
	}
	if marks[0].EvidenceSentenceID == marks[1].EvidenceSentenceID || marks[1].EvidenceSentenceID == marks[2].EvidenceSentenceID {
		t.Fatal("evidence IDs must be unique")
	}
}

func TestBuildFromJSONMapsNormalizedTingwuPayload(t *testing.T) {
	doc, err := BuildFromJSON(RawInput{
		VideoID: "video-3", TranscriptGeneration: "generation-3", Title: "规范化转写", DurationSeconds: 2,
		Provider: "tingwu",
		Payload:  []byte(`{"transcripts":[{"paragraphs":[{"paragraph_id":"p-1","speaker_id":"spk-1","sentences":[{"sentence_id":"s-1","text":"hello","start_ms":0,"end_ms":500}]}]}]}`),
	})
	if err != nil {
		t.Fatalf("BuildFromJSON returned error: %v", err)
	}
	mark := doc.Chapters[0].Paragraphs[0].TimeMarks[0]
	if mark.SourceSentenceID != "s-1" || mark.Text != "hello" || mark.EndMs != 500 {
		t.Fatalf("normalized source mapping was not preserved: %+v", mark)
	}
}

func TestBuildFromJSONMapsSubtitleWorkerPayload(t *testing.T) {
	doc, err := BuildFromJSON(RawInput{
		VideoID: "video-4", TranscriptGeneration: "generation-4", Title: "字幕任务结果", DurationSeconds: 2,
		Provider: "tingwu",
		Payload:  []byte(`{"paragraphs":[{"paragraph_id":"p-1","speaker_id":"spk-1","sentences":[{"sentence_id":"s-1","text":"worker result","start_ms":0,"end_ms":500}]}],"language":"zh"}`),
	})
	if err != nil {
		t.Fatalf("BuildFromJSON returned error: %v", err)
	}
	if got := doc.Chapters[0].Paragraphs[0].Text; got != "worker result" {
		t.Fatalf("worker payload text = %q", got)
	}
}

func TestBuildFromJSONRejectsInvalidBoundaryAndProvider(t *testing.T) {
	cases := []RawInput{
		{VideoID: "video-1", TranscriptGeneration: "generation-1", Title: "标题", Provider: "mps", Payload: []byte(`{"segments":[{"source_segment_id":"seg-1","text":"内容","start_ms":100,"end_ms":100}]}`)},
		{VideoID: "video-1", TranscriptGeneration: "generation-1", Title: "标题", Provider: "unknown", Payload: []byte(`{"segments":[]}`)},
		{VideoID: "video-1", TranscriptGeneration: "generation-1", Title: "标题", Provider: "mps", Payload: []byte(`{"segments":[]}`)},
	}
	for index, input := range cases {
		if _, err := BuildFromJSON(input); err == nil {
			t.Fatalf("case %d unexpectedly succeeded", index)
		}
	}
}

func TestBuildFromJSONIsDeterministicAndJSONValid(t *testing.T) {
	input := RawInput{VideoID: "video-1", TranscriptGeneration: "generation-1", Title: "标题", Provider: "mps", Payload: []byte(`{"segments":[{"text":"hello","start_ms":0,"end_ms":1000}]}`)}
	first, err := BuildFromJSON(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildFromJSON(input)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, err := first.JSON()
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := second.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if firstJSON != secondJSON {
		t.Fatal("same provider payload must produce identical documents")
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(firstJSON), &decoded); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(firstJSON, `"schema_version": 1`) {
		t.Fatalf("schema version missing: %s", firstJSON)
	}
}
