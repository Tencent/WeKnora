package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/Tencent/WeKnora/internal/skillrunner"
)

func main() {
	credential := os.Getenv("WEKNORA_SKILL_RUNNER_CREDENTIAL")
	if credential == "" {
		log.Fatal("WEKNORA_SKILL_RUNNER_CREDENTIAL is required")
	}
	root := os.Getenv("WEKNORA_TENANT_SKILLS_DIR")
	if root == "" {
		root = "/data/skills"
	}
	sourceVolume := os.Getenv("WEKNORA_TENANT_SKILLS_VOLUME")
	if sourceVolume == "" {
		log.Fatal("WEKNORA_TENANT_SKILLS_VOLUME is required")
	}
	executor := skillrunner.NewExecutor(skillrunner.NewFileResolver(root, sourceVolume), 60*time.Second)
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := skillrunner.CleanupOrphans(cleanupCtx); err != nil {
		log.Printf("orphan cleanup warning: %v", err)
	}
	cleanupCancel()
	server := &http.Server{
		Addr: ":8091", Handler: skillrunner.NewHandler(executor, credential),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 70 * time.Second,
	}
	log.Printf("skill runner listening on %s", server.Addr)
	log.Fatal(server.ListenAndServe())
}
