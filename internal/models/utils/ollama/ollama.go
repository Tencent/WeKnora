package ollama

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/ollama/ollama/api"
)

// OllamaService manages Ollama service
type OllamaService struct {
	client      *api.Client
	baseURL     string
	mu          sync.Mutex
	isAvailable bool
	isOptional  bool // Added: marks if Ollama service is optional
}

// GetOllamaService gets Ollama service instance (singleton pattern)
func GetOllamaService() (*OllamaService, error) {
	// Get Ollama base URL from environment variable, if not set use provided baseURL or default value
	logger.GetLogger(context.Background()).Infof("Ollama base URL: %s", os.Getenv("OLLAMA_BASE_URL"))
	baseURL := "http://localhost:11434"
	envURL := os.Getenv("OLLAMA_BASE_URL")
	if envURL != "" {
		baseURL = envURL
	}

	// Create URL object
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid Ollama service URL: %w", err)
	}

	// Create official client
	client := api.NewClient(parsedURL, http.DefaultClient)

	// Check if Ollama is set as optional
	isOptional := false
	if os.Getenv("OLLAMA_OPTIONAL") == "true" {
		isOptional = true
		logger.GetLogger(context.Background()).Info("Ollama service set to optional mode")
	}

	service := &OllamaService{
		client:     client,
		baseURL:    baseURL,
		isOptional: isOptional,
	}

	return service, nil
}

// StartService checks if Ollama service is available
func (s *OllamaService) StartService(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if service is available
	err := s.client.Heartbeat(ctx)
	if err != nil {
		logger.GetLogger(ctx).Warnf("Ollama service unavailable: %v", err)
		s.isAvailable = false

		// If configured as optional, don't return an error
		if s.isOptional {
			logger.GetLogger(ctx).Info("Ollama service set as optional, will continue running the application")
			return nil
		}

		return fmt.Errorf("Ollama service unavailable: %w", err)
	}

	s.isAvailable = true
	logger.GetLogger(ctx).Info("Ollama service ready")
	return nil
}

// IsAvailable returns whether the service is available
func (s *OllamaService) IsAvailable() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isAvailable
}

// IsModelAvailable checks if a model is available
func (s *OllamaService) IsModelAvailable(ctx context.Context, modelName string) (bool, error) {
	// First check if the service is available
	if err := s.StartService(ctx); err != nil {
		return false, err
	}

	// If service is not available but set as optional, return false but no error
	if !s.isAvailable && s.isOptional {
		return false, nil
	}

	// Get model list
	listResp, err := s.client.List(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get model list: %w", err)
	}

	// Check if model is in the list
	for _, model := range listResp.Models {
		if model.Name == modelName {
			return true, nil
		}
	}

	return false, nil
}

// PullModel pulls a model
func (s *OllamaService) PullModel(ctx context.Context, modelName string) error {
	// First check if the service is available
	if err := s.StartService(ctx); err != nil {
		return err
	}

	// If service is not available but set as optional, return nil without further operations
	if !s.isAvailable && s.isOptional {
		logger.GetLogger(ctx).Warnf("Ollama service unavailable, unable to pull model %s", modelName)
		return nil
	}

	// Check if model already exists
	available, err := s.IsModelAvailable(ctx, modelName)
	if err != nil {
		return err
	}
	if available {
		logger.GetLogger(ctx).Infof("Model %s already exists", modelName)
		return nil
	}

	logger.GetLogger(ctx).Infof("Pulling model %s...", modelName)

	// Use official client to pull model
	pullReq := &api.PullRequest{
		Name: modelName,
	}

	err = s.client.Pull(ctx, pullReq, func(progress api.ProgressResponse) error {
		if progress.Status != "" {
			if progress.Total > 0 && progress.Completed > 0 {
				percentage := float64(progress.Completed) / float64(progress.Total) * 100
				logger.GetLogger(ctx).Infof("Pull progress: %s (%.2f%%)",
					progress.Status, percentage)
			} else {
				logger.GetLogger(ctx).Infof("Pull status: %s", progress.Status)
			}
		}

		if progress.Total > 0 && progress.Completed == progress.Total {
			logger.GetLogger(ctx).Infof("Model %s pull completed", modelName)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to pull model: %w", err)
	}

	return nil
}

// EnsureModelAvailable ensures the model is available, pulls it if not available
func (s *OllamaService) EnsureModelAvailable(ctx context.Context, modelName string) error {
	// If service is not available but set as optional, return nil directly
	if !s.IsAvailable() && s.isOptional {
		logger.GetLogger(ctx).Warnf("Ollama service unavailable, skipping ensuring model %s availability", modelName)
		return nil
	}

	available, err := s.IsModelAvailable(ctx, modelName)
	if err != nil {
		if s.isOptional {
			logger.GetLogger(ctx).Warnf("Failed to check model %s availability, but Ollama is set as optional", modelName)
			return nil
		}
		return err
	}

	if !available {
		return s.PullModel(ctx, modelName)
	}

	return nil
}

// GetVersion gets Ollama version
func (s *OllamaService) GetVersion(ctx context.Context) (string, error) {
	// If service is not available but set as optional, return empty version info
	if !s.IsAvailable() && s.isOptional {
		return "unavailable", nil
	}

	version, err := s.client.Version(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get Ollama version: %w", err)
	}
	return version, nil
}

// CreateModel creates a custom model
func (s *OllamaService) CreateModel(ctx context.Context, name, modelfile string) error {
	req := &api.CreateRequest{
		Model:    name,
		Template: modelfile, // Use Template field instead of Modelfile
	}

	err := s.client.Create(ctx, req, func(progress api.ProgressResponse) error {
		if progress.Status != "" {
			logger.GetLogger(ctx).Infof("Model creation status: %s", progress.Status)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create model: %w", err)
	}

	return nil
}

// GetModelInfo gets model information
func (s *OllamaService) GetModelInfo(ctx context.Context, modelName string) (*api.ShowResponse, error) {
	req := &api.ShowRequest{
		Name: modelName,
	}

	resp, err := s.client.Show(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get model information: %w", err)
	}

	return resp, nil
}

// ListModels lists all available models
func (s *OllamaService) ListModels(ctx context.Context) ([]string, error) {
	listResp, err := s.client.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get model list: %w", err)
	}

	modelNames := make([]string, len(listResp.Models))
	for i, model := range listResp.Models {
		modelNames[i] = model.Name
	}

	return modelNames, nil
}

// DeleteModel deletes a model
func (s *OllamaService) DeleteModel(ctx context.Context, modelName string) error {
	req := &api.DeleteRequest{
		Name: modelName,
	}

	err := s.client.Delete(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to delete model: %w", err)
	}

	return nil
}

// IsValidModelName checks if model name is valid
func IsValidModelName(name string) bool {
	// Simple check for model name format
	return name != "" && !strings.Contains(name, " ")
}

// Chat uses Ollama chat
func (s *OllamaService) Chat(ctx context.Context, req *api.ChatRequest, fn api.ChatResponseFunc) error {
	// First check if service is available
	if err := s.StartService(ctx); err != nil {
		return err
	}

	// Use official client Chat method
	return s.client.Chat(ctx, req, fn)
}

// Embeddings gets text embedding vectors
func (s *OllamaService) Embeddings(ctx context.Context, req *api.EmbedRequest) (*api.EmbedResponse, error) {
	// First check if service is available
	if err := s.StartService(ctx); err != nil {
		return nil, err
	}
	// Use official client Embed method
	return s.client.Embed(ctx, req)
}

// Generate generates text (used for Rerank)
func (s *OllamaService) Generate(ctx context.Context, req *api.GenerateRequest, fn api.GenerateResponseFunc) error {
	// First check if service is available
	if err := s.StartService(ctx); err != nil {
		return err
	}

	// Use official client Generate method
	return s.client.Generate(ctx, req, fn)
}

// GetClient returns the underlying ollama client for advanced operations
func (s *OllamaService) GetClient() *api.Client {
	return s.client
}
