package types

import "testing"

func TestSessionListSourceRequiresAdmin(t *testing.T) {
	tests := []struct {
		source string
		want   bool
	}{
		{"", false},
		{"web", false},
		{"WEB", false},
		{"api", true},
		{"embed", true},
		{"embed:ch-1", true},
		{"feishu", true},
		{"qqbot", true},
	}
	for _, tt := range tests {
		if got := SessionListSourceRequiresAdmin(tt.source); got != tt.want {
			t.Fatalf("SessionListSourceRequiresAdmin(%q) = %v, want %v", tt.source, got, tt.want)
		}
	}
}

func TestSessionRequiresAdminConsoleRead(t *testing.T) {
	api := &Session{UserID: SessionOwnerAPITenantKeyPrefix + "1:10"}
	if !SessionRequiresAdminConsoleRead(api, "") {
		t.Fatal("API-key session should require admin")
	}

	embed := &Session{Description: EmbedSessionMarkerPrefix + "ch-1"}
	if !SessionRequiresAdminConsoleRead(embed, "") {
		t.Fatal("embed description should require admin")
	}

	embedOwner := &Session{UserID: PrincipalEmbedSession + ":1:ch-1:sess-1"}
	if !SessionRequiresAdminConsoleRead(embedOwner, "") {
		t.Fatal("embed owner id should require admin")
	}

	im := &Session{Title: "hello"}
	if !SessionRequiresAdminConsoleRead(im, "feishu") {
		t.Fatal("IM-mapped session should require admin")
	}

	web := &Session{UserID: "alice", Title: "my chat"}
	if SessionRequiresAdminConsoleRead(web, "") {
		t.Fatal("personal web session should not require admin")
	}
}
