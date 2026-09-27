package host

import "testing"

func TestParseStat(t *testing.T) {
	// A name with spaces and a parenthesis; pgrp 4242, rss 1536 pages.
	line := "4250 (weird (name) x) S 4242 4242 4242 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 3 0 1000 " +
		"123456789 1536 18446744073709551615 1 1 0 0 0 0 0 0 0 0 0 0 17 3 0 0 0 0 0"
	pgrp, rss, ok := parseStat([]byte(line))
	if !ok || pgrp != 4242 || rss != 1536 {
		t.Fatalf("parseStat = %d, %d, %v", pgrp, rss, ok)
	}
	if _, _, ok := parseStat([]byte("garbage")); ok {
		t.Fatal("garbage parsed")
	}
}
