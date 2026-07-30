package types

import "strings"

// WebSearchConfigForResponse returns a copy safe for HTTP responses.
// When maskSecrets is true, api_key is omitted and a configured proxy_url
// is replaced with RedactedSecretPlaceholder.
func WebSearchConfigForResponse(cfg *WebSearchConfig, maskSecrets bool) *WebSearchConfig {
	if cfg == nil {
		return nil
	}
	out := *EffectiveWebSearchConfig(cfg)
	if !maskSecrets {
		return &out
	}
	out.APIKey = ""
	if strings.TrimSpace(out.ProxyURL) != "" {
		out.ProxyURL = RedactedSecretPlaceholder
	}
	return &out
}

// ParserEngineConfigForResponse returns a copy with secret fields redacted
// when maskSecrets is true.
func ParserEngineConfigForResponse(cfg *ParserEngineConfig, maskSecrets bool) *ParserEngineConfig {
	if cfg == nil {
		return nil
	}
	out := *cfg
	if !maskSecrets {
		return &out
	}
	if out.MinerUAPIKey != "" {
		out.MinerUAPIKey = RedactedSecretPlaceholder
	}
	if out.PaddleOCRVLCloudToken != "" {
		out.PaddleOCRVLCloudToken = RedactedSecretPlaceholder
	}
	return &out
}

// StorageEngineConfigForResponse returns a copy with provider secret fields
// redacted when maskSecrets is true.
func StorageEngineConfigForResponse(cfg *StorageEngineConfig, maskSecrets bool) *StorageEngineConfig {
	if cfg == nil {
		return nil
	}
	out := *cfg
	if !maskSecrets {
		return &out
	}
	if out.MinIO != nil {
		minio := *out.MinIO
		if minio.AccessKeyID != "" {
			minio.AccessKeyID = RedactedSecretPlaceholder
		}
		if minio.SecretAccessKey != "" {
			minio.SecretAccessKey = RedactedSecretPlaceholder
		}
		out.MinIO = &minio
	}
	if out.COS != nil {
		cos := *out.COS
		if cos.SecretID != "" {
			cos.SecretID = RedactedSecretPlaceholder
		}
		if cos.SecretKey != "" {
			cos.SecretKey = RedactedSecretPlaceholder
		}
		out.COS = &cos
	}
	if out.TOS != nil {
		tos := *out.TOS
		if tos.AccessKey != "" {
			tos.AccessKey = RedactedSecretPlaceholder
		}
		if tos.SecretKey != "" {
			tos.SecretKey = RedactedSecretPlaceholder
		}
		out.TOS = &tos
	}
	if out.S3 != nil {
		s3 := *out.S3
		if s3.AccessKey != "" {
			s3.AccessKey = RedactedSecretPlaceholder
		}
		if s3.SecretKey != "" {
			s3.SecretKey = RedactedSecretPlaceholder
		}
		out.S3 = &s3
	}
	if out.OSS != nil {
		oss := *out.OSS
		if oss.AccessKey != "" {
			oss.AccessKey = RedactedSecretPlaceholder
		}
		if oss.SecretKey != "" {
			oss.SecretKey = RedactedSecretPlaceholder
		}
		out.OSS = &oss
	}
	if out.KS3 != nil {
		ks3 := *out.KS3
		if ks3.AccessKey != "" {
			ks3.AccessKey = RedactedSecretPlaceholder
		}
		if ks3.SecretKey != "" {
			ks3.SecretKey = RedactedSecretPlaceholder
		}
		out.KS3 = &ks3
	}
	if out.OBS != nil {
		obs := *out.OBS
		if obs.AccessKey != "" {
			obs.AccessKey = RedactedSecretPlaceholder
		}
		if obs.SecretKey != "" {
			obs.SecretKey = RedactedSecretPlaceholder
		}
		out.OBS = &obs
	}
	return &out
}

// CredentialsConfigForResponse returns a copy with app_secret redacted when
// maskSecrets is true.
func CredentialsConfigForResponse(cfg *CredentialsConfig, maskSecrets bool) *CredentialsConfig {
	if cfg == nil {
		return nil
	}
	out := *cfg
	if !maskSecrets {
		return &out
	}
	if out.WeKnoraCloud != nil {
		cloud := *out.WeKnoraCloud
		if cloud.AppSecret != "" {
			cloud.AppSecret = RedactedSecretPlaceholder
		}
		out.WeKnoraCloud = &cloud
	}
	return &out
}

// MergeWebSearchConfigForUpdate applies preserve semantics to secret fields on
// tenant KV PUT.
func MergeWebSearchConfigForUpdate(incoming, existing *WebSearchConfig) *WebSearchConfig {
	out := *EffectiveWebSearchConfig(incoming)
	var prev WebSearchConfig
	if existing != nil {
		prev = *EffectiveWebSearchConfig(existing)
	}
	out.APIKey = PreserveIfRedacted(out.APIKey, prev.APIKey)
	out.ProxyURL = PreserveIfRedacted(out.ProxyURL, prev.ProxyURL)
	return &out
}

// MergeParserEngineConfigForUpdate applies preserve semantics to secret fields
// on tenant KV PUT.
func MergeParserEngineConfigForUpdate(incoming, existing *ParserEngineConfig) *ParserEngineConfig {
	if incoming == nil {
		return nil
	}
	out := *incoming
	var prev ParserEngineConfig
	if existing != nil {
		prev = *existing
	}
	out.MinerUAPIKey = PreserveIfRedacted(out.MinerUAPIKey, prev.MinerUAPIKey)
	out.PaddleOCRVLCloudToken = PreserveIfRedacted(out.PaddleOCRVLCloudToken, prev.PaddleOCRVLCloudToken)
	// Chat attachment parser rules are configured per agent; preserve any legacy
	// tenant-level rules when the settings UI omits this field on engine updates.
	if incoming.ChatParserEngineRules == nil && existing != nil {
		out.ChatParserEngineRules = existing.ChatParserEngineRules
	}
	return &out
}

// MergeStorageEngineConfigForUpdate applies preserve semantics to provider
// secret fields on tenant KV PUT.
func MergeStorageEngineConfigForUpdate(incoming, existing *StorageEngineConfig) *StorageEngineConfig {
	if incoming == nil {
		return nil
	}
	out := *incoming
	if out.MinIO != nil {
		minio := *out.MinIO
		var prev MinIOEngineConfig
		if existing != nil && existing.MinIO != nil {
			prev = *existing.MinIO
		}
		minio.AccessKeyID = PreserveIfRedacted(minio.AccessKeyID, prev.AccessKeyID)
		minio.SecretAccessKey = PreserveIfRedacted(minio.SecretAccessKey, prev.SecretAccessKey)
		out.MinIO = &minio
	}
	if out.COS != nil {
		cos := *out.COS
		var prev COSEngineConfig
		if existing != nil && existing.COS != nil {
			prev = *existing.COS
		}
		cos.SecretID = PreserveIfRedacted(cos.SecretID, prev.SecretID)
		cos.SecretKey = PreserveIfRedacted(cos.SecretKey, prev.SecretKey)
		out.COS = &cos
	}
	if out.TOS != nil {
		tos := *out.TOS
		var prev TOSEngineConfig
		if existing != nil && existing.TOS != nil {
			prev = *existing.TOS
		}
		tos.AccessKey = PreserveIfRedacted(tos.AccessKey, prev.AccessKey)
		tos.SecretKey = PreserveIfRedacted(tos.SecretKey, prev.SecretKey)
		out.TOS = &tos
	}
	if out.S3 != nil {
		s3 := *out.S3
		var prev S3EngineConfig
		if existing != nil && existing.S3 != nil {
			prev = *existing.S3
		}
		s3.AccessKey = PreserveIfRedacted(s3.AccessKey, prev.AccessKey)
		s3.SecretKey = PreserveIfRedacted(s3.SecretKey, prev.SecretKey)
		out.S3 = &s3
	}
	if out.OSS != nil {
		oss := *out.OSS
		var prev OSSEngineConfig
		if existing != nil && existing.OSS != nil {
			prev = *existing.OSS
		}
		oss.AccessKey = PreserveIfRedacted(oss.AccessKey, prev.AccessKey)
		oss.SecretKey = PreserveIfRedacted(oss.SecretKey, prev.SecretKey)
		out.OSS = &oss
	}
	if out.KS3 != nil {
		ks3 := *out.KS3
		var prev KS3EngineConfig
		if existing != nil && existing.KS3 != nil {
			prev = *existing.KS3
		}
		ks3.AccessKey = PreserveIfRedacted(ks3.AccessKey, prev.AccessKey)
		ks3.SecretKey = PreserveIfRedacted(ks3.SecretKey, prev.SecretKey)
		out.KS3 = &ks3
	}
	if out.OBS != nil {
		obs := *out.OBS
		var prev OBSEngineConfig
		if existing != nil && existing.OBS != nil {
			prev = *existing.OBS
		}
		obs.AccessKey = PreserveIfRedacted(obs.AccessKey, prev.AccessKey)
		obs.SecretKey = PreserveIfRedacted(obs.SecretKey, prev.SecretKey)
		out.OBS = &obs
	}
	return &out
}
