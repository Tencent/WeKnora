package sandbox

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	sandboxLabelKey     = "weknora-sandbox"
	sandboxLabelValue   = "true"
	sandboxCreatedAtKey = "weknora-sandbox-created-at"
	gcOrphanAge         = 30 * time.Minute
	gcTickerInterval    = 5 * time.Minute
	gcListTimeout       = 30 * time.Second
	gcDeleteTimeout     = 5 * time.Second
	cleanupTimeout      = 10 * time.Second
	jobPollInterval     = 500 * time.Millisecond
)

// KubernetesSandbox implements the Sandbox interface using Kubernetes Jobs.
type KubernetesSandbox struct {
	config    *Config
	mu        sync.Mutex
	client    *kubeClient
	semaphore chan struct{}
	stopCh    chan struct{}
	stopOnce  sync.Once
}

// NewKubernetesSandbox creates a new KubernetesSandbox.
// If config is nil, DefaultConfig() is used.
// The client may be nil; it will be initialized lazily in IsAvailable/Execute.
func NewKubernetesSandbox(config *Config, client *kubeClient) *KubernetesSandbox {
	if config == nil {
		config = DefaultConfig()
	}

	s := &KubernetesSandbox{
		config:    config,
		client:    client,
		semaphore: make(chan struct{}, config.MaxConcurrentSandboxes),
		stopCh:    make(chan struct{}),
	}

	go s.gcOrphanResources()

	return s
}

// Type returns the sandbox type.
func (s *KubernetesSandbox) Type() SandboxType {
	return SandboxTypeKubernetes
}

// IsAvailable checks if the Kubernetes sandbox is available.
func (s *KubernetesSandbox) IsAvailable(ctx context.Context) bool {
	s.mu.Lock()
	if s.client == nil {
		if !inClusterAvailable() {
			s.mu.Unlock()
			return false
		}
		c, err := newKubeClientInCluster()
		if err != nil {
			s.mu.Unlock()
			return false
		}
		s.client = c
	}
	c := s.client
	s.mu.Unlock()
	return c.checkAccess(ctx, s.config.KubeNamespace)
}

// getClient returns the current kubeClient under the mutex.
func (s *KubernetesSandbox) getClient() *kubeClient {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.client
}

// Execute runs a script in an ephemeral Kubernetes Job.
func (s *KubernetesSandbox) Execute(ctx context.Context, config *ExecuteConfig) (*ExecuteResult, error) {
	if config == nil {
		return nil, ErrInvalidScript
	}

	scriptContent, scriptName, err := resolveScript(config)
	if err != nil {
		return nil, err
	}

	if s.config.MaxScriptSize > 0 && int64(len(scriptContent)) > s.config.MaxScriptSize {
		return nil, fmt.Errorf("%w: script size %d exceeds limit %d", ErrInvalidScript, len(scriptContent), s.config.MaxScriptSize)
	}

	client := s.getClient()
	if client == nil {
		return nil, fmt.Errorf("kubernetes sandbox: client not initialized (call IsAvailable first)")
	}

	select {
	case s.semaphore <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-s.semaphore }()

	timeout := config.Timeout
	if timeout == 0 {
		timeout = s.config.DefaultTimeout
	}
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resourceName := fmt.Sprintf("weknora-sandbox-%s", uuid.New().String()[:8])

	labels := map[string]string{
		sandboxLabelKey:     sandboxLabelValue,
		sandboxCreatedAtKey: strconv.FormatInt(time.Now().Unix(), 10),
	}

	cmData := map[string]string{
		scriptName: scriptContent,
	}
	if config.Stdin != "" {
		cmData[stdinFileName] = config.Stdin
	}

	if err := client.createConfigMap(execCtx, s.config.KubeNamespace, resourceName, cmData, labels); err != nil {
		return nil, fmt.Errorf("failed to create ConfigMap: %w", err)
	}

	defer func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cleanCancel()
		_ = client.deleteJob(cleanCtx, s.config.KubeNamespace, resourceName)
		_ = client.deleteConfigMap(cleanCtx, s.config.KubeNamespace, resourceName)
	}()

	interpreter := getInterpreter(scriptName)
	scriptPath := "/workspace/" + scriptName

	// Build command: use exec-form when possible, shell-form only when stdin piping is needed
	var command []string
	if config.Stdin != "" {
		command = []string{"sh", "-c", buildShellCommand(interpreter, scriptPath, config.Args, true)}
	} else {
		command = buildExecCommand(interpreter, scriptPath, config.Args)
	}

	memoryLimit := s.config.MaxMemory
	if config.MemoryLimit > 0 {
		memoryLimit = config.MemoryLimit
	}
	memoryLimitStr := fmt.Sprintf("%dMi", memoryLimit/(1024*1024))

	cpuLimit := s.config.MaxCPU
	if config.CPULimit > 0 {
		cpuLimit = config.CPULimit
	}
	cpuLimitStr := fmt.Sprintf("%dm", int(cpuLimit*1000))

	spec := &jobSpec{
		Name:               resourceName,
		Image:              s.config.DockerImage,
		Command:            command,
		ConfigMapName:      resourceName,
		TimeoutSeconds:     int(timeout.Seconds()),
		ServiceAccountName: s.config.KubeServiceAccount,
		MemoryLimit:        memoryLimitStr,
		CPULimit:           cpuLimitStr,
	}

	if err := client.createJob(execCtx, s.config.KubeNamespace, spec); err != nil {
		return nil, fmt.Errorf("failed to create Job: %w", err)
	}

	startTime := time.Now()

	pollTicker := time.NewTicker(jobPollInterval)
	defer pollTicker.Stop()

	var finalStatus *jobStatus
	for {
		select {
		case <-execCtx.Done():
			return &ExecuteResult{
				ExitCode: -1,
				Killed:   true,
				Error:    ErrTimeout.Error(),
				Duration: time.Since(startTime),
			}, nil
		case <-pollTicker.C:
		}

		status, err := client.getJobStatus(execCtx, s.config.KubeNamespace, resourceName)
		if err != nil {
			// Context may have been cancelled
			if execCtx.Err() != nil {
				return &ExecuteResult{
					ExitCode: -1,
					Killed:   true,
					Error:    ErrTimeout.Error(),
					Duration: time.Since(startTime),
				}, nil
			}
			continue
		}

		if status.succeeded || status.failed {
			finalStatus = status
			break
		}
	}

	duration := time.Since(startTime)

	exitCode := 0
	if finalStatus != nil && finalStatus.failed {
		exitCode = 1
	}

	podName, err := client.findJobPod(execCtx, s.config.KubeNamespace, resourceName)
	if err != nil {
		return &ExecuteResult{
			ExitCode: exitCode,
			Duration: duration,
			Error:    fmt.Sprintf("failed to find pod: %v", err),
		}, nil
	}

	logs, err := client.getPodLogs(execCtx, s.config.KubeNamespace, podName, s.config.MaxLogSize)
	if err != nil {
		return &ExecuteResult{
			ExitCode: exitCode,
			Duration: duration,
			Error:    fmt.Sprintf("failed to get pod logs: %v", err),
		}, nil
	}

	return &ExecuteResult{
		Stdout:   logs,
		ExitCode: exitCode,
		Duration: duration,
	}, nil
}

// Cleanup stops the background GC goroutine and releases sandbox resources.
// Safe to call multiple times.
func (s *KubernetesSandbox) Cleanup(ctx context.Context) error {
	s.stopOnce.Do(func() { close(s.stopCh) })
	return nil
}

// gcOrphanResources periodically cleans up orphaned ConfigMaps left behind by failed executions.
// It uses the weknora-sandbox-created-at label to determine age and only deletes resources
// older than gcOrphanAge. Normal cleanup is handled by defer in Execute; this catches leaks.
func (s *KubernetesSandbox) gcOrphanResources() {
	ticker := time.NewTicker(gcTickerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
		}

		client := s.getClient()
		if client == nil {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), gcListTimeout)
		labelSelector := sandboxLabelKey + "=" + sandboxLabelValue
		cms, err := client.listConfigMapsWithLabels(ctx, s.config.KubeNamespace, labelSelector)
		cancel()

		if err != nil {
			continue
		}

		now := time.Now().Unix()
		for _, cm := range cms {
			createdAtStr, ok := cm.labels[sandboxCreatedAtKey]
			if !ok {
				continue
			}
			createdAt, err := strconv.ParseInt(createdAtStr, 10, 64)
			if err != nil {
				continue
			}
			if now-createdAt < int64(gcOrphanAge.Seconds()) {
				continue
			}

			cleanCtx, cleanCancel := context.WithTimeout(context.Background(), gcDeleteTimeout)
			_ = client.deleteConfigMap(cleanCtx, s.config.KubeNamespace, cm.name)
			cleanCancel()
		}
	}
}
