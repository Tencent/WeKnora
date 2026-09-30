package middleware

import (
	"sync"
	"testing"
	"time"
)

func TestIPRateLimiterCleanupPreservesActiveBudget(t *testing.T) {
	limiter := &ipRateLimiter{window: time.Minute, max: 1}
	key := "198.51.100.10"
	if !limiter.allow(key) {
		t.Fatal("first request must be allowed")
	}
	limiter.cleanupOnce(time.Now().Add(-limiter.window))
	if limiter.allow(key) {
		t.Fatal("cleanup reset an active IP's budget")
	}
}

func TestIPRateLimiterCleanupRemovesExpiredBucket(t *testing.T) {
	limiter := &ipRateLimiter{window: time.Minute, max: 1}
	key := "198.51.100.11"
	limiter.buckets = map[string]*ipBucket{
		key: {timestamps: []time.Time{time.Now().Add(-2 * time.Minute)}},
	}
	limiter.cleanupOnce(time.Now().Add(-limiter.window))
	if _, ok := limiter.buckets[key]; ok {
		t.Fatal("expired bucket was not removed")
	}
	if !limiter.allow(key) {
		t.Fatal("request after expiry must be allowed")
	}
}

func TestIPRateLimiterConcurrentCleanupPreservesBudget(t *testing.T) {
	limiter := &ipRateLimiter{window: time.Minute, max: 1}
	key := "198.51.100.12"
	limiter.buckets = map[string]*ipBucket{
		key: {timestamps: []time.Time{time.Now().Add(-2 * time.Minute)}},
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		limiter.cleanupOnce(time.Now().Add(-limiter.window))
	}()
	go func() {
		defer wg.Done()
		limiter.allow(key)
	}()
	wg.Wait()
	if limiter.allow(key) {
		t.Fatal("concurrent cleanup lost the request's budget")
	}
}
