package container

import (
	"bytes"
	"os"
	"testing"

	"github.com/Tencent/WeKnora/internal/handler"
	"go.uber.org/dig"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSkillComponentsRegisterBeforeAgentService(t *testing.T) {
	source, err := os.ReadFile("container.go")
	if err != nil {
		t.Fatal(err)
	}
	skillComponents := bytes.Index(source, []byte("must(registerSkillComponents(container))"))
	agentService := bytes.Index(source, []byte("must(container.Provide(service.NewAgentService))"))
	if skillComponents < 0 || agentService < 0 || skillComponents > agentService {
		t.Fatalf("skill components must register before AgentService: skills=%d agent=%d", skillComponents, agentService)
	}
}

func TestSkillContainerContractResolvesHandlerInLiteMode(t *testing.T) {
	t.Setenv("DB_DRIVER", "sqlite")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	container := dig.New()
	if err := container.Provide(func() *gorm.DB { return db }); err != nil {
		t.Fatal(err)
	}
	if err := registerSkillComponents(container); err != nil {
		t.Fatal(err)
	}
	if err := container.Invoke(func(skillHandler *handler.SkillHandler) {
		if skillHandler == nil {
			t.Fatal("skill handler was not resolved")
		}
	}); err != nil {
		t.Fatal(err)
	}
}
