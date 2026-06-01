package chat

import (
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// resolveImageURLForLLM converts stored image paths to a format that LLM APIs can consume.
// - data: URIs and http(s):// URLs are returned as-is.
// - local:// paths are read from disk and converted to base64 data URIs.
func resolveImageURLForLLM(imageURL string) string {
	if strings.HasPrefix(imageURL, "data:") || strings.HasPrefix(imageURL, "http://") || strings.HasPrefix(imageURL, "https://") {
		return imageURL
	}
	if strings.HasPrefix(imageURL, "local://") {
		data := readLocalStorageBytes(imageURL)
		if data != nil {
			mime := http.DetectContentType(data)
			return fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(data))
		}
	}
	return imageURL
}

// resolveImageURLForOllama converts stored image paths to raw bytes for the Ollama API.
func resolveImageURLForOllama(imageURL string) []byte {
	if strings.HasPrefix(imageURL, "data:") {
		idx := strings.Index(imageURL, ";base64,")
		if idx < 0 {
			return nil
		}
		decoded, err := base64.StdEncoding.DecodeString(imageURL[idx+8:])
		if err != nil {
			return nil
		}
		return decoded
	}
	if strings.HasPrefix(imageURL, "local://") {
		return readLocalStorageBytes(imageURL)
	}
	return nil
}

// LocalImageResolver, when set by the application layer at startup, resolves a
// local:// storage URL to its bytes using the owning tenant's storage config.
// Stored local:// URLs are relative to the storage base dir and do NOT encode
// the tenant's configured PathPrefix, so a plain env-based join would miss the
// prefix. When nil (e.g. in tests), callers fall back to the env-based
// LOCAL_STORAGE_BASE_DIR resolution below.
var LocalImageResolver func(storageURL string) ([]byte, bool)

// readLocalStorageBytes resolves a local:// storage path to disk bytes.
func readLocalStorageBytes(storagePath string) []byte {
	if LocalImageResolver != nil {
		if data, ok := LocalImageResolver(storagePath); ok {
			return data
		}
	}
	relPath := strings.TrimPrefix(storagePath, "local://")
	baseDir := os.Getenv("LOCAL_STORAGE_BASE_DIR")
	if baseDir == "" {
		baseDir = "/data/files"
	}
	localPath := filepath.Join(baseDir, filepath.FromSlash(relPath))
	data, err := os.ReadFile(localPath)
	if err != nil {
		log.Printf("[image-resolve] failed to read local file %s: %v", localPath, err)
		return nil
	}
	return data
}

// isMultimodalNotSupportedError checks if an error indicates the model does not
// support multimodal/image input.
func isMultimodalNotSupportedError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return (strings.Contains(msg, "multimodal") || strings.Contains(msg, "image") || strings.Contains(msg, "vision")) &&
		(strings.Contains(msg, "not support") || strings.Contains(msg, "unsupported") || strings.Contains(msg, "400"))
}

// stripImagesFromMessages returns a copy of messages with all image data removed.
func stripImagesFromMessages(messages []Message) []Message {
	cleaned := make([]Message, len(messages))
	for i, msg := range messages {
		cleaned[i] = msg
		cleaned[i].Images = nil
	}
	return cleaned
}
