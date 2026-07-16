package wecom_chat_archive

import (
	"fmt"
	"strings"
)

const redactedSecret = "[REDACTED]"

func sanitizeConnectorError(cfg *Config, err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if cfg != nil {
		for _, value := range []string{cfg.Secret, cfg.PrivateKey} {
			trimmed := strings.TrimSpace(value)
			if trimmed != "" {
				message = strings.ReplaceAll(message, trimmed, redactedSecret)
			}
		}
	}
	message = redactPEMMarkers(message)
	return fmt.Errorf("%s", message)
}

func redactPEMMarkers(message string) string {
	markers := []string{
		"-----BEGIN PRIVATE KEY-----",
		"-----END PRIVATE KEY-----",
		"-----BEGIN RSA PRIVATE KEY-----",
		"-----END RSA PRIVATE KEY-----",
		"BEGIN PRIVATE KEY",
		"END PRIVATE KEY",
		"PRIVATE KEY",
	}
	for _, marker := range markers {
		message = strings.ReplaceAll(message, marker, redactedSecret)
	}
	return message
}
