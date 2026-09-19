package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Tencent/WeKnora/internal/plugin"
)

func main() {
	socketPath := envOrDefault("WEKNORA_PLUGIN_RUNTIME_AGENT_SOCKET", "/run/weknora/plugin-runtime/agent.sock")
	runtimeRoot := envOrDefault("WEKNORA_PLUGIN_RUNTIME_ROOT", "/run/weknora/plugin-runtime")
	token := os.Getenv("WEKNORA_PLUGIN_RUNTIME_AGENT_TOKEN")
	instanceID := defaultInstanceID()

	imagePolicy, err := plugin.NewImagePolicyFromEnv()
	if err != nil {
		log.Fatalf("runtime agent: %v", err)
	}

	server := plugin.NewRuntimeAgentServer(runtimeRoot, token, plugin.LoggerAuditSink{}, imagePolicy, instanceID)
	if err := server.Serve(socketPath); err != nil {
		log.Fatalf("runtime agent: %v", err)
	}
	log.Printf("runtime agent listening on %s (runtime root %s)", socketPath, runtimeRoot)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	log.Println("runtime agent shutting down")
	_ = server.Shutdown(context.Background())
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// defaultInstanceID derives a unique runtime-instance label so multiple WeKnora
// deployments sharing one Docker daemon never clean up each other's containers.
// Explicit config wins; otherwise the Compose project name, then the hostname
// (the container name under compose) are used before the generic fallback.
func defaultInstanceID() string {
	if v := os.Getenv("WEKNORA_PLUGIN_RUNTIME_INSTANCE_ID"); v != "" {
		return v
	}
	if v := os.Getenv("COMPOSE_PROJECT_NAME"); v != "" {
		return v
	}
	if hn, err := os.Hostname(); err == nil && hn != "" {
		return hn
	}
	return "weknora"
}
