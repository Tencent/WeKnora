package service

import (
	"context"
	"testing"

)

func TestIsEmbedSessionToken(t *testing.T) {
	if !IsEmbedSessionToken("ems_abc123") {
		t.Fatal("expected ems_ prefix to be session token")
	}
	if IsEmbedSessionToken("em_abc123") {
		t.Fatal("publish token must not be treated as session token")
	}
	if IsEmbedSessionToken("") {
		t.Fatal("empty token must not match")
	}
}

func TestIssueSessionTokenWithoutRedis(t *testing.T) {
	svc := &embedChannelService{redis: nil}
	_, _, err := svc.IssueSessionToken(context.Background(), "channel-1")
	if err != ErrEmbedSessionUnavailable {
		t.Fatalf("expected ErrEmbedSessionUnavailable, got %v", err)
	}
}

func TestResolveSessionTokenWithoutRedis(t *testing.T) {
	svc := &embedChannelService{redis: nil}
	_, err := svc.ResolveSessionToken(context.Background(), "ems_test")
	if err != ErrEmbedSessionUnavailable {
		t.Fatalf("expected ErrEmbedSessionUnavailable, got %v", err)
	}
}

func TestResolveSessionTokenRejectsPublishToken(t *testing.T) {
	svc := &embedChannelService{redis: nil}
	_, err := svc.ResolveSessionToken(context.Background(), "em_publish_only")
	if err != ErrEmbedTokenInvalid {
		t.Fatalf("expected ErrEmbedTokenInvalid for publish token, got %v", err)
	}
}
