package types

import "strings"

// deploymentParserDefaults holds the deployment-wide parser defaults loaded
// from config.yaml (parser_defaults). They only fill gaps: explicit knowledge
// base or agent parser rules and tenant parser settings always win.
var deploymentParserDefaults struct {
	engine    string
	fileTypes map[string]struct{}
	overrides map[string]string
}

// SetDeploymentParserDefaults installs the deployment parser defaults loaded
// from config.yaml. Call it once at startup; zero values disable the feature.
// An empty file type list disables only the default engine.
func SetDeploymentParserDefaults(engine string, fileTypes []string, overrides map[string]string) {
	engine = strings.TrimSpace(engine)
	deploymentParserDefaults.engine = ""
	deploymentParserDefaults.fileTypes = nil
	if engine != "" {
		set := make(map[string]struct{}, len(fileTypes))
		for _, fileType := range fileTypes {
			if normalized := normalizeParserFileType(fileType); normalized != "" {
				set[normalized] = struct{}{}
			}
		}
		if len(set) > 0 {
			deploymentParserDefaults.engine = engine
			deploymentParserDefaults.fileTypes = set
		}
	}

	deploymentParserDefaults.overrides = nil
	if len(overrides) > 0 {
		// Copy so a caller that later mutates its config map cannot change the
		// installed defaults behind the parse path's back.
		copied := make(map[string]string, len(overrides))
		for key, value := range overrides {
			copied[key] = value
		}
		deploymentParserDefaults.overrides = copied
	}
}

// deploymentParserEngineFor returns the configured deployment engine when it
// covers the normalized file type.
func deploymentParserEngineFor(fileType string) (string, bool) {
	if deploymentParserDefaults.engine == "" {
		return "", false
	}
	if _, ok := deploymentParserDefaults.fileTypes[fileType]; !ok {
		return "", false
	}
	return deploymentParserDefaults.engine, true
}

// ParserOverridesFor merges deployment parser overrides under tenant values.
// A nil config still receives the deployment overrides.
func ParserOverridesFor(cfg *ParserEngineConfig) map[string]string {
	tenantOverrides := cfg.ToOverridesMap()
	if len(deploymentParserDefaults.overrides) == 0 {
		return tenantOverrides
	}
	merged := make(map[string]string, len(deploymentParserDefaults.overrides)+len(tenantOverrides))
	for key, value := range deploymentParserDefaults.overrides {
		merged[key] = value
	}
	for key, value := range tenantOverrides {
		merged[key] = value
	}
	return merged
}
