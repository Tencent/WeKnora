package service

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestWithCanonicalIdentityLockSerializesAcrossInstances(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()
	firstRedis := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	secondRedis := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer firstRedis.Close()
	defer secondRedis.Close()
	first := &wikiPageService{redisClient: firstRedis}
	second := &wikiPageService{redisClient: secondRedis}

	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- first.WithCanonicalIdentityLock(context.Background(), "kb-1", "个人知识库五关标准", func(context.Context) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered

	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- second.WithCanonicalIdentityLock(context.Background(), "kb-1", "个人知识库五大条件", func(context.Context) error {
			close(secondEntered)
			return nil
		})
	}()
	select {
	case <-secondEntered:
		t.Fatal("second instance entered canonical write lock before first released")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("second instance did not acquire canonical write lock after release")
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}
