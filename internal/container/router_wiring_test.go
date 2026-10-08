package container

import (
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/dig"
)

// Resolve the production dependency graph without opening connections or
// starting background workers. Handler tests alone do not catch missing DI types.
func TestRouterWiring(t *testing.T) {
	for _, mode := range []struct {
		name      string
		redisAddr string
	}{
		{name: "lite"},
		{name: "redis", redisAddr: "localhost:6379"},
	} {
		t.Run(mode.name, func(t *testing.T) {
			t.Setenv("REDIS_ADDR", mode.redisAddr)
			c := BuildContainer(dig.New(dig.DryRun(true)))
			if err := c.Invoke(func(*gin.Engine) {}); err != nil {
				t.Fatalf("resolve router dependencies: %v", err)
			}
		})
	}
}
