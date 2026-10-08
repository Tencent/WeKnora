package api

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ResolveImageURLForLLM converts stored image paths to a format that LLM APIs can consume.
// - data: URIs and http(s):// URLs are returned as-is.
// - resource:// and provider-backed local paths are read through the
// application resolver and converted to base64 data URIs.
func ResolveImageURLForLLM(imageURL string) string {
	if strings.HasPrefix(imageURL, "data:") ||
		strings.HasPrefix(imageURL, "http://") ||
		strings.HasPrefix(imageURL, "https://") {
		return imageURL
	}
	if isApplicationStoredImage(imageURL) {
		data := readLocalStorageBytes(imageURL)
		if data != nil {
			mime := http.DetectContentType(data)
			return fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(data))
		}
	}
	return imageURL
}

// ResolveImageURLForOllama converts stored image paths to raw bytes for the Ollama API.
func ResolveImageURLForOllama(imageURL string) []byte {
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
	if isApplicationStoredImage(imageURL) {
		return readLocalStorageBytes(imageURL)
	}
	return nil
}

func isApplicationStoredImage(imageURL string) bool {
	return strings.HasPrefix(imageURL, "resource://") ||
		strings.HasPrefix(imageURL, "local://") ||
		strings.HasPrefix(imageURL, "storage://")
}

// LocalImageResolver resolves a resource:// or provider storage URL to bytes
// using the owning tenant's storage config. The application layer sets it at
// startup.
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

// IsMultimodalNotSupportedError checks if an error indicates the model does not
// support multimodal/image input.
//
// The status is read off the error, never matched as text: HTTPError.Error()
// prints "status 400" for every 400, and vendor bodies carry "400" inside
// unrelated numbers ("max 4000 px"), so a substring test turned any failure
// that merely mentions an image into a "model is not multimodal" verdict and
// silently retried it stripped.
func IsMultimodalNotSupportedError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	mentionsImages := strings.Contains(msg, "multimodal") ||
		strings.Contains(msg, "image") ||
		strings.Contains(msg, "vision")
	if !mentionsImages {
		return false
	}
	if strings.Contains(msg, "not support") || strings.Contains(msg, "unsupported") {
		return true
	}
	// No explicit wording: only a genuine 400 (the status vendors use to reject
	// an input their model cannot read) is worth a stripped retry.
	var httpErr *HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusBadRequest
}

// StripImagesFromMessages returns a copy of messages with all image data removed.
//
// Images travel in two shapes: the Images list (chat history, Ollama) and
// image_url parts inside MultiContent, which is what the remote VLM path and
// every assembled multimodal turn actually send. Clearing only the first left
// the retry sending the identical request, so the "retry without images"
// fallback never recovered anything on those paths.
func StripImagesFromMessages(messages []Message) []Message {
	cleaned := make([]Message, len(messages))
	for i, msg := range messages {
		cleaned[i] = msg
		cleaned[i].Images = nil
		cleaned[i].MultiContent = stripImageParts(msg.MultiContent)
	}
	return cleaned
}

// stripImageParts drops image parts from one MultiContent list, copying it
// only when there is something to drop so the caller's slice is never mutated.
// A list that was nothing but images becomes nil: the protocol packages then
// fall back to the message's plain Content instead of sending an empty parts
// array, which some vendors reject.
func stripImageParts(parts []MessageContentPart) []MessageContentPart {
	if len(parts) == 0 {
		return parts
	}
	kept := make([]MessageContentPart, 0, len(parts))
	for _, part := range parts {
		if part.Type == "image_url" || part.ImageURL != nil {
			continue
		}
		kept = append(kept, part)
	}
	if len(kept) == len(parts) {
		return parts
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}
