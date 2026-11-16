package container

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/processingcache"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"go.uber.org/dig"
	"gorm.io/gorm"
)

type processingCacheHandlerSpy struct{ cache *processingcache.Runtime }

func (s *processingCacheHandlerSpy) SetProcessingCache(r *processingcache.Runtime) { s.cache = r }
func (s *processingCacheHandlerSpy) Handle(context.Context, *asynq.Task) error     { return nil }

var _ interfaces.TaskHandler = (*processingCacheHandlerSpy)(nil)

func TestWireProcessingCache(t *testing.T) {
	c := dig.New()
	a, b := &processingCacheHandlerSpy{}, &processingCacheHandlerSpy{}
	imageHandler := &processingCacheHandlerSpy{}
	if err := c.Provide(
		func() interfaces.TaskHandler { return imageHandler },
		dig.Name("imageMultimodal"),
	); err != nil {
		t.Fatal(err)
	}
	if err := c.Provide(func() interfaces.TaskHandler { return a }, dig.Name("chunkExtractor")); err != nil {
		t.Fatal(err)
	}
	if err := c.Provide(func() interfaces.TaskHandler { return b }, dig.Name("wikiIngest")); err != nil {
		t.Fatal(err)
	}
	// A nil knowledge dependency is sufficient: the attachment is optional.
	if err := c.Provide(func() interfaces.KnowledgeService { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := c.Provide(func() *gorm.DB { return &gorm.DB{} }); err != nil {
		t.Fatal(err)
	}
	if err := c.Provide(func() *config.Config {
		return &config.Config{ProcessingCache: &config.ProcessingCacheConfig{ReadEnabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.Invoke(wireProcessingCache); err != nil {
		t.Fatal(err)
	}
	if a.cache == nil || a.cache != b.cache || imageHandler.cache != a.cache {
		t.Fatal("handlers must share one runtime")
	}
}
