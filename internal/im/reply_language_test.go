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
	if got, _ := types.LanguageFromContext(ctx); got != "vi" {
		t.Fatalf("Vietnamese turn language = %q, want vi", got)
	}
	ctx = s.withIMReplyLanguage(context.Background(), cs, &IncomingMessage{MessageType: MessageTypeText, Content: "ok"})
	if got, _ := types.LanguageFromContext(ctx); got != "vi" {
		t.Fatalf("short turn language = %q, want remembered vi", got)
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

func TestFollowUserLanguagePersistsLocaleCode(t *testing.T) {
	t.Setenv("WEKNORA_LANGUAGE", "en-US")
	db := newLifecycleTestDB(t)
	if err := db.Exec(`CREATE TABLE im_channel_sessions (
		id TEXT PRIMARY KEY, last_detected_language TEXT NOT NULL DEFAULT '', deleted_at DATETIME
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO im_channel_sessions(id) VALUES ('sess')`).Error; err != nil {
		t.Fatal(err)
	}
	s := &Service{db: db}
	cs := &ChannelSession{ID: "sess"}
	s.withIMReplyLanguage(context.Background(), cs, &IncomingMessage{
		MessageType: MessageTypeText,
		Content:     "我想查询我的订单信息，请帮我确认目前的配送状态。",
	})
	var persisted string
	if err := db.Raw(`SELECT last_detected_language FROM im_channel_sessions WHERE id = 'sess'`).
		Scan(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	if persisted != "zh-CN" {
		t.Fatalf("persisted language = %q, want zh-CN", persisted)
	}
	ctx := s.withIMReplyLanguage(context.Background(), cs, &IncomingMessage{MessageType: MessageTypeText, Content: "ok"})
	if got, _ := types.LanguageFromContext(ctx); got != "zh-CN" {
		t.Fatalf("remembered language = %q, want zh-CN", got)
	}
}
