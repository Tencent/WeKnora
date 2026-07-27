package userinput

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func validQuestion(mode Mode) Question {
	return Question{
		Text:       "公司是以什么方式通知你解除劳动合同的？",
		Mode:       mode,
		GroupID:    "dismissal-facts",
		Index:      1,
		Total:      3,
		AllowOther: true,
		AllowSkip:  true,
		Options: []Option{
			{ID: "written", Label: "书面通知", Description: "公司出具了解除通知书"},
			{ID: "verbal", Label: "口头通知"},
		},
	}
}

func collectionQuestion(mode Mode) Question {
	return Question{
		Text: "请补充信息", Mode: mode, GroupID: "collection-agent-1", Index: 12, Total: 100,
		FieldKey: "dismissal_date", SchemaVersion: 3, CompletedCount: 11, RemainingCount: 2,
		AllowSkip: false,
	}
}

func TestValidateQuestionAcceptsValidModes(t *testing.T) {
	for _, mode := range []Mode{ModeSingle, ModeMultiple} {
		q := validQuestion(mode)
		if err := ValidateQuestion(q); err != nil {
			t.Fatalf("ValidateQuestion(%s) error = %v", mode, err)
		}
	}
}

func TestValidateQuestionAcceptsCollectionValueModesAndDynamicCounts(t *testing.T) {
	for _, mode := range []Mode{ModeShortText, ModeLongText, ModeNumber, ModeDate} {
		q := collectionQuestion(mode)
		if err := ValidateQuestion(q); err != nil {
			t.Fatalf("ValidateQuestion(%s) error = %v", mode, err)
		}
	}
}

func TestValidateQuestionRejectsInvalidFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Question)
		want   string
	}{
		{name: "empty question", mutate: func(q *Question) { q.Text = " " }, want: "question"},
		{name: "question too long", mutate: func(q *Question) { q.Text = strings.Repeat("问", 501) }, want: "question"},
		{name: "invalid mode", mutate: func(q *Question) { q.Mode = Mode("text") }, want: "mode"},
		{name: "invalid group id", mutate: func(q *Question) { q.GroupID = "劳动 争议" }, want: "question_group_id"},
		{name: "index below one", mutate: func(q *Question) { q.Index = 0 }, want: "question_index"},
		{name: "total above schema cap", mutate: func(q *Question) { q.Total = 101 }, want: "question_total"},
		{name: "index above total", mutate: func(q *Question) { q.Index = 4 }, want: "question_index"},
		{name: "too few options", mutate: func(q *Question) { q.Options = q.Options[:1] }, want: "options"},
		{name: "too many options", mutate: func(q *Question) {
			q.Options = make([]Option, 9)
			for i := range q.Options {
				q.Options[i] = Option{ID: "option_" + string(rune('a'+i)), Label: "选项"}
			}
		}, want: "options"},
		{name: "duplicate option id", mutate: func(q *Question) { q.Options[1].ID = q.Options[0].ID }, want: "unique"},
		{name: "invalid option id", mutate: func(q *Question) { q.Options[0].ID = "bad id" }, want: "option id"},
		{name: "empty option label", mutate: func(q *Question) { q.Options[0].Label = "" }, want: "label"},
		{name: "long option label", mutate: func(q *Question) { q.Options[0].Label = strings.Repeat("选", 121) }, want: "label"},
		{name: "long option description", mutate: func(q *Question) { q.Options[0].Description = strings.Repeat("说", 301) }, want: "description"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := validQuestion(ModeSingle)
			tt.mutate(&q)
			err := ValidateQuestion(q)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidateQuestion() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestValidateAnswerAcceptsSupportedResponses(t *testing.T) {
	tests := []struct {
		name   string
		q      Question
		answer Answer
	}{
		{name: "single choice", q: validQuestion(ModeSingle), answer: Answer{SelectedOptionIDs: []string{"written"}}},
		{name: "single other", q: validQuestion(ModeSingle), answer: Answer{OtherText: "公司直接停用了账号"}},
		{name: "multiple choices and other", q: validQuestion(ModeMultiple), answer: Answer{SelectedOptionIDs: []string{"written", "verbal"}, OtherText: "另有邮件"}},
		{name: "skip", q: validQuestion(ModeSingle), answer: Answer{Skipped: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateAnswer(tt.q, tt.answer); err != nil {
				t.Fatalf("ValidateAnswer() error = %v", err)
			}
		})
	}
}

func TestValidateAnswerAcceptsTypedCollectionValues(t *testing.T) {
	maxLength := 20
	tests := []struct {
		mode  Mode
		value any
	}{
		{mode: ModeShortText, value: "协商解除"},
		{mode: ModeLongText, value: "公司在会议中说明了原因"},
		{mode: ModeNumber, value: float64(3)},
		{mode: ModeDate, value: "2026-07-22"},
	}
	for _, tt := range tests {
		q := collectionQuestion(tt.mode)
		q.Validation = types.AgentCollectionValidation{MaxLength: &maxLength}
		answer := Answer{FieldKey: q.FieldKey, SchemaVersion: q.SchemaVersion, Value: tt.value}
		if err := ValidateAnswer(q, answer); err != nil {
			t.Fatalf("ValidateAnswer(%s) error = %v", tt.mode, err)
		}
	}
}

func TestValidateAnswerRejectsMismatchedCollectionMetadataAndValues(t *testing.T) {
	tests := []struct {
		name   string
		mode   Mode
		answer Answer
		want   string
	}{
		{name: "field mismatch", mode: ModeShortText, answer: Answer{FieldKey: "other", SchemaVersion: 3, Value: "说明"}, want: "field_key"},
		{name: "schema mismatch", mode: ModeShortText, answer: Answer{FieldKey: "dismissal_date", SchemaVersion: 2, Value: "说明"}, want: "schema_version"},
		{name: "invalid number", mode: ModeNumber, answer: Answer{FieldKey: "dismissal_date", SchemaVersion: 3, Value: "three"}, want: "number"},
		{name: "invalid date", mode: ModeDate, answer: Answer{FieldKey: "dismissal_date", SchemaVersion: 3, Value: "22/07/2026"}, want: "date"},
		{name: "choice in text mode", mode: ModeShortText, answer: Answer{FieldKey: "dismissal_date", SchemaVersion: 3, SelectedOptionIDs: []string{"written"}, Value: "说明"}, want: "selection"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAnswer(collectionQuestion(tt.mode), tt.answer)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidateAnswer() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestValidateAnswerRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Question, *Answer)
		want   string
	}{
		{name: "empty answer", mutate: func(_ *Question, _ *Answer) {}, want: "answer"},
		{name: "unknown option", mutate: func(_ *Question, a *Answer) { a.SelectedOptionIDs = []string{"missing"} }, want: "unknown"},
		{name: "duplicate selection", mutate: func(_ *Question, a *Answer) { a.SelectedOptionIDs = []string{"written", "written"} }, want: "duplicate"},
		{name: "multiple selected in single mode", mutate: func(_ *Question, a *Answer) { a.SelectedOptionIDs = []string{"written", "verbal"} }, want: "single_choice"},
		{name: "single selection with other", mutate: func(_ *Question, a *Answer) { a.SelectedOptionIDs = []string{"written"}; a.OtherText = "其他" }, want: "single_choice"},
		{name: "other disabled", mutate: func(q *Question, a *Answer) { q.AllowOther = false; a.OtherText = "其他" }, want: "other_text"},
		{name: "other too long", mutate: func(_ *Question, a *Answer) { a.OtherText = strings.Repeat("其", 1001) }, want: "other_text"},
		{name: "skip disabled", mutate: func(q *Question, a *Answer) { q.AllowSkip = false; a.Skipped = true }, want: "skip"},
		{name: "skip with selection", mutate: func(_ *Question, a *Answer) { a.Skipped = true; a.SelectedOptionIDs = []string{"written"} }, want: "skip"},
		{name: "skip with other", mutate: func(_ *Question, a *Answer) { a.Skipped = true; a.OtherText = "其他" }, want: "skip"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := validQuestion(ModeSingle)
			answer := Answer{}
			tt.mutate(&q, &answer)
			err := ValidateAnswer(q, answer)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidateAnswer() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}
