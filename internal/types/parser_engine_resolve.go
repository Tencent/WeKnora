// internal/types/parser_engine_resolve.go
package types

import (
	"fmt"
	"os"
	"strings"
)

// ---------------------------------------------------------------------------
// Engine-level package merging: if the tenant key field is non-empty, use the entire tenant set; otherwise, use the entire builtin set.
// ---------------------------------------------------------------------------

// ResolveMinerUOverrides returns the mineru_* overrides map.
// Tenant wins as a whole when tenant.MinerUEndpoint is non-empty; otherwise
// fall back to the builtin.MinerU block as a whole.
func ResolveMinerUOverrides(tenant *ParserEngineConfig) map[string]string {
	if tenant != nil && strings.TrimSpace(tenant.MinerUEndpoint) != "" {
		return mineruTenantToMap(tenant)
	}
	if b := GetBuiltinParserEngine(); b != nil && b.MinerU != nil {
		return mineruBuiltinToMap(b.MinerU)
	}
	return nil
}

// ResolveMinerUCloudOverrides returns mineru_cloud_* overrides map.
// Tenant wins when tenant.MinerUAPIKey is non-empty.
func ResolveMinerUCloudOverrides(tenant *ParserEngineConfig) map[string]string {
	if tenant != nil && strings.TrimSpace(tenant.MinerUAPIKey) != "" {
		return mineruCloudTenantToMap(tenant)
	}
	if b := GetBuiltinParserEngine(); b != nil && b.MinerUCloud != nil {
		return mineruCloudBuiltinToMap(b.MinerUCloud)
	}
	return nil
}

// ResolveWeKnoraCloudAppID returns the effective weknoracloud_app_id.
// tenantCredsAppID (from tenant.Credentials.WeKnoraCloud.AppID) wins; otherwise
// builtin.WeKnoraCloud.AppID is used.
func ResolveWeKnoraCloudAppID(tenantCredsAppID string) string {
	if v := strings.TrimSpace(tenantCredsAppID); v != "" {
		return v
	}
	if b := GetBuiltinParserEngine(); b != nil && b.WeKnoraCloud != nil {
		return strings.TrimSpace(b.WeKnoraCloud.AppID)
	}
	return ""
}

// ResolveDocReaderAddr returns the effective docreader address.
// envValue (typically os.Getenv("DOCREADER_ADDR")) wins; otherwise
// builtin.DocReaderAddr is used. Empty string means "let caller use code default".
func ResolveDocReaderAddr(envValue string) string {
	if v := strings.TrimSpace(envValue); v != "" {
		return v
	}
	if b := GetBuiltinParserEngine(); b != nil {
		return strings.TrimSpace(b.DocReaderAddr)
	}
	return ""
}

// MergeParserEngineOverrides is the one-stop API for handler / service code.
// It returns the final overrides map (snake_case keys) used by docreader
// requests and engine availability checks. The map already includes
// docreader_addr (when non-empty) and weknoracloud_app_id (when non-empty).
//
// tenant may be nil. If tenant has its own MinerU* fields or Credentials,
// those win field-by-field as described in ResolveXxxOverrides.
func MergeParserEngineOverrides(tenant *Tenant) map[string]string {
	out := map[string]string{}

	var tenantCfg *ParserEngineConfig
	var credsAppID string
	if tenant != nil {
		tenantCfg = tenant.ParserEngineConfig
		if creds := tenant.Credentials.GetWeKnoraCloud(); creds != nil {
			credsAppID = creds.AppID
		}
	}

	for k, v := range ResolveMinerUOverrides(tenantCfg) {
		out[k] = v
	}
	for k, v := range ResolveMinerUCloudOverrides(tenantCfg) {
		out[k] = v
	}
	if appID := ResolveWeKnoraCloudAppID(credsAppID); appID != "" {
		out["weknoracloud_app_id"] = appID
	}
	if addr := ResolveDocReaderAddr(os.Getenv("DOCREADER_ADDR")); addr != "" {
		out["docreader_addr"] = addr
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

// ---------------------------------------------------------------------------
// Map writers — keep snake_case keys consistent with ParserEngineConfig.ToOverridesMap.
// ---------------------------------------------------------------------------

func mineruTenantToMap(c *ParserEngineConfig) map[string]string {
	m := map[string]string{
		"mineru_endpoint": c.MinerUEndpoint,
	}
	if c.MinerUModel != "" {
		m["mineru_model"] = c.MinerUModel
	}
	if c.MinerUVLMServerURL != "" {
		m["mineru_vlm_server_url"] = c.MinerUVLMServerURL
	}
	if c.MinerUEnableFormula != nil {
		m["mineru_enable_formula"] = fmt.Sprintf("%v", *c.MinerUEnableFormula)
	}
	if c.MinerUEnableTable != nil {
		m["mineru_enable_table"] = fmt.Sprintf("%v", *c.MinerUEnableTable)
	}
	if c.MinerUEnableOCR != nil {
		m["mineru_enable_ocr"] = fmt.Sprintf("%v", *c.MinerUEnableOCR)
	}
	if c.MinerULanguage != "" {
		m["mineru_language"] = c.MinerULanguage
	}
	return m
}

func mineruBuiltinToMap(b *BuiltinMinerUConfig) map[string]string {
	if b.Endpoint == "" {
		return nil
	}
	m := map[string]string{
		"mineru_endpoint": b.Endpoint,
	}
	if b.Model != "" {
		m["mineru_model"] = b.Model
	}
	if b.VLMServerURL != "" {
		m["mineru_vlm_server_url"] = b.VLMServerURL
	}
	if b.EnableFormula != nil {
		m["mineru_enable_formula"] = fmt.Sprintf("%v", *b.EnableFormula)
	}
	if b.EnableTable != nil {
		m["mineru_enable_table"] = fmt.Sprintf("%v", *b.EnableTable)
	}
	if b.EnableOCR != nil {
		m["mineru_enable_ocr"] = fmt.Sprintf("%v", *b.EnableOCR)
	}
	if b.Language != "" {
		m["mineru_language"] = b.Language
	}
	return m
}

func mineruCloudTenantToMap(c *ParserEngineConfig) map[string]string {
	m := map[string]string{
		"mineru_api_key": c.MinerUAPIKey,
	}
	if c.MinerUCloudModel != "" {
		m["mineru_cloud_model"] = c.MinerUCloudModel
	}
	if c.MinerUCloudEnableFormula != nil {
		m["mineru_cloud_enable_formula"] = fmt.Sprintf("%v", *c.MinerUCloudEnableFormula)
	}
	if c.MinerUCloudEnableTable != nil {
		m["mineru_cloud_enable_table"] = fmt.Sprintf("%v", *c.MinerUCloudEnableTable)
	}
	if c.MinerUCloudEnableOCR != nil {
		m["mineru_cloud_enable_ocr"] = fmt.Sprintf("%v", *c.MinerUCloudEnableOCR)
	}
	if c.MinerUCloudLanguage != "" {
		m["mineru_cloud_language"] = c.MinerUCloudLanguage
	}
	return m
}

func mineruCloudBuiltinToMap(b *BuiltinMinerUCloudConfig) map[string]string {
	if b.APIKey == "" {
		return nil
	}
	m := map[string]string{
		"mineru_api_key": b.APIKey,
	}
	if b.Model != "" {
		m["mineru_cloud_model"] = b.Model
	}
	if b.EnableFormula != nil {
		m["mineru_cloud_enable_formula"] = fmt.Sprintf("%v", *b.EnableFormula)
	}
	if b.EnableTable != nil {
		m["mineru_cloud_enable_table"] = fmt.Sprintf("%v", *b.EnableTable)
	}
	if b.EnableOCR != nil {
		m["mineru_cloud_enable_ocr"] = fmt.Sprintf("%v", *b.EnableOCR)
	}
	if b.Language != "" {
		m["mineru_cloud_language"] = b.Language
	}
	return m
}
