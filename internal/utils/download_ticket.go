package utils

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DownloadTicketPrefix  = "wdt1."
	DownloadTicketPurpose = "knowledge_download"
	DownloadTicketTTL     = 5 * time.Minute
	DownloadTicketSkew    = 30 * time.Second
	DefaultTicketRenewGrace = time.Hour
	MaxTicketRenewGrace     = 24 * time.Hour
)

type DownloadTicketClaims struct {
	Purpose     string
	KnowledgeID string
	TenantID    uint64
	Expires     int64
}

func canonicalDownloadTicket(purpose, knowledgeID string, tenantID uint64, expires int64) string {
	return fmt.Sprintf("purpose=%s&knowledge_id=%s&tenant_id=%d&expires=%d",
		purpose, knowledgeID, tenantID, expires)
}

// SignKnowledgeDownloadTicket mints a 5-minute HMAC ticket bound to the
// knowledge row owner tenant. Returns ("", err) when SYSTEM_AES_KEY is missing.
func SignKnowledgeDownloadTicket(knowledgeID string, tenantID uint64, now time.Time) (ticket string, expiresAt time.Time, err error) {
	key := SystemHMACKey()
	if key == nil {
		return "", time.Time{}, fmt.Errorf("download ticket: SYSTEM_AES_KEY not configured")
	}
	if now.IsZero() {
		now = time.Now()
	}
	expiresAt = now.Add(DownloadTicketTTL)
	canonical := canonicalDownloadTicket(DownloadTicketPurpose, knowledgeID, tenantID, expiresAt.Unix())
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(canonical))
	sig := hex.EncodeToString(mac.Sum(nil))
	encoded := base64.RawURLEncoding.EncodeToString([]byte(canonical))
	return DownloadTicketPrefix + encoded + "." + sig, expiresAt, nil
}

// ParseKnowledgeDownloadTicket verifies HMAC and returns claims. expiredOK
// allows already-expired tickets (renew path); caller still checks grace.
func ParseKnowledgeDownloadTicket(ticket string, now time.Time, expiredOK bool) (*DownloadTicketClaims, error) {
	key := SystemHMACKey()
	if key == nil {
		return nil, fmt.Errorf("download ticket: SYSTEM_AES_KEY not configured")
	}
	if !strings.HasPrefix(ticket, DownloadTicketPrefix) {
		return nil, fmt.Errorf("invalid ticket")
	}
	rest := strings.TrimPrefix(ticket, DownloadTicketPrefix)
	dot := strings.LastIndex(rest, ".")
	if dot <= 0 || dot == len(rest)-1 {
		return nil, fmt.Errorf("invalid ticket")
	}
	encoded, sigHex := rest[:dot], rest[dot+1:]
	canonical, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("invalid ticket")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(canonical)
	want := mac.Sum(nil)
	got, err := hex.DecodeString(sigHex)
	if err != nil || !hmac.Equal(got, want) {
		return nil, fmt.Errorf("invalid ticket")
	}
	claims, err := parseDownloadCanonical(string(canonical))
	if err != nil {
		return nil, err
	}
	if claims.Purpose != DownloadTicketPurpose {
		return nil, fmt.Errorf("invalid ticket purpose")
	}
	if now.IsZero() {
		now = time.Now()
	}
	if !expiredOK && now.Unix() > claims.Expires+int64(DownloadTicketSkew.Seconds()) {
		return nil, fmt.Errorf("ticket expired")
	}
	return claims, nil
}

func parseDownloadCanonical(canonical string) (*DownloadTicketClaims, error) {
	parts := strings.Split(canonical, "&")
	got := map[string]string{}
	for _, part := range parts {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("invalid ticket")
		}
		got[k] = v
	}
	tenantID, err := strconv.ParseUint(got["tenant_id"], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid ticket")
	}
	expires, err := strconv.ParseInt(got["expires"], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid ticket")
	}
	return &DownloadTicketClaims{
		Purpose:     got["purpose"],
		KnowledgeID: got["knowledge_id"],
		TenantID:    tenantID,
		Expires:     expires,
	}, nil
}

func TicketRenewGrace() time.Duration {
	raw := strings.TrimSpace(os.Getenv("WEBHOOK_DOWNLOAD_TICKET_RENEW_GRACE"))
	if raw == "" {
		return DefaultTicketRenewGrace
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return DefaultTicketRenewGrace
	}
	if d > MaxTicketRenewGrace {
		return MaxTicketRenewGrace
	}
	return d
}
