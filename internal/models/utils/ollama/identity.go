package ollama

// BaseURL returns the immutable endpoint captured when this service was built.
func (s *OllamaService) BaseURL() string {
	if s == nil {
		return ""
	}
	return s.baseURL
}
