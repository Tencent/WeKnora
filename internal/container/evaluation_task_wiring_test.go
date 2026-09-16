package container

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"go.uber.org/dig"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestEvaluationTaskRepositoryWiring(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory database: %v", err)
	}

	container := dig.New()
	if err := container.Provide(func() *gorm.DB { return db }); err != nil {
		t.Fatalf("provide database: %v", err)
	}
	if err := container.Provide(repository.NewEvaluationTaskRepository); err != nil {
		t.Fatalf("provide evaluation task repository: %v", err)
	}
	if err := container.Invoke(func(taskRepository interfaces.EvaluationTaskRepository) {
		if taskRepository == nil {
			t.Fatal("resolved evaluation task repository is nil")
		}
	}); err != nil {
		t.Fatalf("resolve evaluation task repository: %v", err)
	}
}
