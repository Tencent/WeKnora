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
