package service

import "testing"

func TestValidateParserOverrideURLsRejectsEverySupportedURLKey(t *testing.T) {
	for _, key := range parserOutboundURLKeys {
		t.Run(key, func(t *testing.T) {
			err := validateParserEngineOverrideURLs(map[string]string{
				key: "http://169.254.169.254/latest/meta-data",
			})
			if err == nil {
				t.Fatalf("expected %s to be rejected", key)
			}
		})
	}
}

func TestValidateParserOverrideURLsIgnoresNonURLKeys(t *testing.T) {
	if err := validateParserEngineOverrideURLs(map[string]string{
		"mineru_model_version": "pipeline",
	}); err != nil {
		t.Fatalf("unexpected error for non-URL override: %v", err)
	}
}
