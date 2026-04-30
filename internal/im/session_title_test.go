package im

import "testing"

func TestShortID(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"shorter than 8", "abc", "abc"},
		{"exactly 8", "12345678", "12345678"},
		{"longer than 8 keeps suffix", "aaaaaaaaXXXXXXXX", "XXXXXXXX"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shortID(tt.in); got != tt.want {
				t.Errorf("shortID(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestBuildUserSessionTitle(t *testing.T) {
	tests := []struct {
		name string
		msg  *IncomingMessage
		want string
	}{
		{
			name: "group with username",
			msg: &IncomingMessage{
				Platform: "feishu",
				UserName: "李四",
				ChatType: ChatTypeGroup,
				ChatID:   "oc_aaaaaaaaaaaaaaaa",
			},
			want: "李四 · group aaaaaaaa",
		},
		{
			name: "direct message with username",
			msg: &IncomingMessage{
				Platform: "feishu",
				UserName: "李四",
				ChatType: ChatTypeDirect,
			},
			want: "李四 · dm",
		},
		{
			name: "group without username falls back to user id",
			msg: &IncomingMessage{
				Platform: "wecom",
				UserID:   "WeCom_ZhangSan",
				ChatType: ChatTypeGroup,
				ChatID:   "wc_group_1234",
			},
			want: "user ZhangSan · group oup_1234",
		},
		{
			name: "no user identity at all",
			msg: &IncomingMessage{
				Platform: "slack",
				ChatType: ChatTypeDirect,
			},
			want: "user · dm",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildUserSessionTitle(tt.msg); got != tt.want {
				t.Errorf("buildUserSessionTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildThreadSessionTitle(t *testing.T) {
	tests := []struct {
		name string
		msg  *IncomingMessage
		want string
	}{
		{
			name: "chat + thread",
			msg: &IncomingMessage{
				Platform: "slack",
				ChatID:   "C0123456789",
				ThreadID: "1700000000.111000",
			},
			want: "chat 23456789 · thread 0.111000",
		},
		{
			name: "thread without chat id",
			msg: &IncomingMessage{
				Platform: "feishu",
				ThreadID: "om_thread_abcdefgh",
			},
			want: "thread abcdefgh",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildThreadSessionTitle(tt.msg); got != tt.want {
				t.Errorf("buildThreadSessionTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}
