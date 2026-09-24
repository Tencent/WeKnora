package im

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestFollowUserLanguageFallsBackWithinSessionAndClearResetsIt(t *testing.T) {
	t.Setenv("WEKNORA_LANGUAGE", "en-US")
	db := newLifecycleTestDB(t)
	if err := db.Exec(`CREATE TABLE im_channel_sessions (
		id TEXT PRIMARY KEY, last_detected_language TEXT NOT NULL DEFAULT '', deleted_at DATETIME
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO im_channel_sessions(id) VALUES ('old')`).Error; err != nil {
		t.Fatal(err)
	}
	s := &Service{db: db}
	cs := &ChannelSession{ID: "old"}
	ctx := s.withIMReplyLanguage(context.Background(), cs, &IncomingMessage{
		MessageType: MessageTypeText,
		Content: "Tôi muốn tìm thông tin về đơn hàng của mình. " +
			"Bạn có thể giúp tôi kiểm tra trạng thái giao hàng không?",
	})
	if got, _ := types.LanguageFromContext(ctx); got != "Vietnamese" {
		t.Fatalf("Vietnamese turn language = %q", got)
	}
	ctx = s.withIMReplyLanguage(context.Background(), cs, &IncomingMessage{MessageType: MessageTypeText, Content: "ok"})
	if got, _ := types.LanguageFromContext(ctx); got != "Vietnamese" {
		t.Fatalf("short turn language = %q, want remembered Vietnamese", got)
	}
	if err := db.Exec(`UPDATE im_channel_sessions SET deleted_at = ? WHERE id = 'old'`, time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO im_channel_sessions(id) VALUES ('new')`).Error; err != nil {
		t.Fatal(err)
	}
	ctx = s.withIMReplyLanguage(context.Background(), &ChannelSession{ID: "new"}, &IncomingMessage{
		MessageType: MessageTypeText,
		Content:     "ok",
	})
	if got, _ := types.LanguageFromContext(ctx); got != "en-US" {
		t.Fatalf("new session language = %q, want deployment default", got)
	}
}
