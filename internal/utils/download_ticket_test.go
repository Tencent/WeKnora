package utils

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"testing"
	"time"
)

func TestKnowledgeDownloadTicketRoundTrip(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef")
	now := time.Unix(1_700_000_000, 0)
	ticket, exp, err := SignKnowledgeDownloadTicket("kid-1", 42, now)
	if err != nil {
		t.Fatal(err)
	}
	if exp.Unix() != now.Add(DownloadTicketTTL).Unix() {
		t.Fatalf("expires = %v", exp)
	}
	claims, err := ParseKnowledgeDownloadTicket(ticket, now.Add(time.Minute), false)
	if err != nil {
		t.Fatal(err)
	}
	if claims.KnowledgeID != "kid-1" || claims.TenantID != 42 || claims.Purpose != DownloadTicketPurpose {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestKnowledgeDownloadTicketExpiredAndWrongID(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef")
	now := time.Unix(1_700_000_000, 0)
	ticket, _, err := SignKnowledgeDownloadTicket("kid-1", 42, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseKnowledgeDownloadTicket(ticket, now.Add(6*time.Minute), false); err == nil {
		t.Fatal("expired ticket must fail")
	}
	claims, err := ParseKnowledgeDownloadTicket(ticket, now.Add(6*time.Minute), true)
	if err != nil {
		t.Fatalf("expiredOK: %v", err)
	}
	if claims.KnowledgeID != "kid-1" {
		t.Fatalf("claims = %+v", claims)
	}
	other, _, err := SignKnowledgeDownloadTicket("kid-2", 42, now)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseKnowledgeDownloadTicket(other, now, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.KnowledgeID == "kid-1" {
		t.Fatal("tickets must bind knowledge id")
	}
}

func TestKnowledgeDownloadTicketRejectsWrongPurpose(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef")
	now := time.Unix(1_700_000_000, 0)
	canonical := canonicalDownloadTicket("other", "kid-1", 42, now.Add(time.Minute).Unix())
	mac := hmac.New(sha256.New, SystemHMACKey())
	mac.Write([]byte(canonical))
	sig := hex.EncodeToString(mac.Sum(nil))
	ticket := DownloadTicketPrefix + base64.RawURLEncoding.EncodeToString([]byte(canonical)) + "." + sig
	if _, err := ParseKnowledgeDownloadTicket(ticket, now, false); err == nil {
		t.Fatal("wrong purpose must fail")
	}
}

func TestKnowledgeDownloadTicketRenewGrace(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef")
	now := time.Unix(1_700_000_000, 0)
	ticket, _, err := SignKnowledgeDownloadTicket("kid-1", 42, now)
	if err != nil {
		t.Fatal(err)
	}
	expiredAt := now.Add(DownloadTicketTTL + 2*time.Minute)
	if _, err := ParseKnowledgeDownloadTicket(ticket, expiredAt, true); err != nil {
		t.Fatalf("expired within grace parse: %v", err)
	}
	tooOld := now.Add(DownloadTicketTTL + DefaultTicketRenewGrace + time.Minute)
	claims, err := ParseKnowledgeDownloadTicket(ticket, tooOld, true)
	if err != nil {
		t.Fatalf("expiredOK still parses: %v", err)
	}
	if tooOld.Unix() <= claims.Expires+int64(TicketRenewGrace().Seconds()) {
		t.Fatal("fixture should be beyond grace")
	}
}

func TestKnowledgeDownloadTicketRequiresKey(t *testing.T) {
	os.Unsetenv("SYSTEM_AES_KEY")
	if _, _, err := SignKnowledgeDownloadTicket("kid-1", 42, time.Now()); err == nil {
		t.Fatal("missing key must fail")
	}
}
