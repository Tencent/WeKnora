package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	semverPattern      = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*))?(\.(0|[1-9][0-9]*))?`)
	ErrManifestInvalid = errors.New("invalid plugin manifest")
)

func (m Manifest) EffectiveNetworkPolicy() NetworkPolicy {
	if m.Permissions.Network == "" {
		return NetworkNone
	}
	return m.Permissions.Network
}

// Validate checks the portable part of the manifest. Host version matching is
// intentionally small and deterministic: manifests can use *, ^MAJOR.MINOR,
// or whitespace-separated comparator ranges such as ">=1.2 <2.0".
func (m Manifest) Validate(hostVersion string) error {
	if m.APIVersion != APIVersionV1 {
		return fmt.Errorf("%w: api_version must be %q", ErrManifestInvalid, APIVersionV1)
	}
	for field, value := range map[string]string{
		"id": m.ID, "name": m.Name, "version": m.Version,
		"extension_type": m.ExtensionType, "protocol_version": m.ProtocolVersion,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: %s is required", ErrManifestInvalid, field)
		}
	}
	if !validID(m.ID) {
		return fmt.Errorf("%w: id %q must contain only lowercase letters, digits, dot, dash or underscore", ErrManifestInvalid, m.ID)
	}
	if !semverPattern.MatchString(m.Version) {
		return fmt.Errorf("%w: version %q is not a semantic version", ErrManifestInvalid, m.Version)
	}
	if m.ProtocolVersion != ProtocolVersionV1 {
		return fmt.Errorf("%w: unsupported protocol_version %q", ErrManifestInvalid, m.ProtocolVersion)
	}
	switch m.ExtensionType {
	case ExtensionDataSource, ExtensionParser, ExtensionSearch, ExtensionModel, ExtensionRetriever:
	default:
		return fmt.Errorf("%w: unsupported extension_type %q", ErrManifestInvalid, m.ExtensionType)
	}
	if m.ExtensionType == ExtensionParser {
		if len(metadataStringList(m.Metadata, "file_types")) == 0 {
			return fmt.Errorf("%w: parser plugins require metadata.file_types", ErrManifestInvalid)
		}
	}
	switch m.EffectiveNetworkPolicy() {
	case NetworkNone, NetworkAllowlist:
	default:
		return fmt.Errorf("%w: unsupported network policy %q", ErrManifestInvalid, m.Permissions.Network)
	}
	if m.EffectiveNetworkPolicy() == NetworkAllowlist && len(m.Permissions.AllowedDestinations) == 0 {
		return fmt.Errorf("%w: allowlist network policy requires allowed_destinations", ErrManifestInvalid)
	}
	if err := validateDataPermissions(m.Permissions.Data); err != nil {
		return fmt.Errorf("%w: %w", ErrManifestInvalid, err)
	}
	if err := validateManifestConfigSchema(m.ExtensionType, m.ConfigSchema); err != nil {
		return err
	}
	if m.WeKnoraVersion != "" && hostVersion != "" && !versionRangeMatches(m.WeKnoraVersion, hostVersion) {
		return fmt.Errorf("%w: host version %q does not satisfy %q", ErrManifestInvalid, hostVersion, m.WeKnoraVersion)
	}
	return nil
}

func validateDataPermissions(data *DataPermissions) error {
	if data == nil {
		return nil
	}
	for field, values := range map[string][]string{
		"permissions.data.tenants":         data.Tenants,
		"permissions.data.knowledge_bases": data.KnowledgeBases,
		"permissions.data.data_sources":    data.DataSources,
	} {
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("%s cannot contain an empty scope", field)
			}
		}
	}
	return nil
}

func metadataStringList(metadata map[string]any, key string) []string {
	if metadata == nil {
		return nil
	}
	values := make([]string, 0)
	switch raw := metadata[key].(type) {
	case []string:
		for _, value := range raw {
			if value = strings.TrimSpace(value); value != "" {
				values = append(values, value)
			}
		}
	case []any:
		for _, value := range raw {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				values = append(values, strings.TrimSpace(text))
			}
		}
	}
	return values
}

func validID(id string) bool {
	if id == "" || id[0] == '-' || id[len(id)-1] == '-' {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '.' && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func parseVersion(value string) (major, minor, patch int, ok bool) {
	value = strings.TrimSpace(strings.TrimPrefix(value, "v"))
	m := semverPattern.FindStringSubmatch(value)
	if m == nil {
		return 0, 0, 0, false
	}
	major, _ = strconv.Atoi(m[1])
	if m[3] != "" {
		minor, _ = strconv.Atoi(m[3])
	}
	if m[5] != "" {
		patch, _ = strconv.Atoi(m[5])
	}
	return major, minor, patch, true
}

func versionRangeMatches(expr, host string) bool {
	hm, hn, hp, ok := parseVersion(host)
	if !ok {
		return false
	}
	expr = strings.TrimSpace(expr)
	if expr == "" || expr == "*" {
		return true
	}
	if strings.HasPrefix(expr, "^") {
		mm, mn, _, ok := parseVersion(strings.TrimPrefix(expr, "^"))
		return ok && hm == mm && hn >= mn
	}
	for _, token := range strings.Fields(expr) {
		op := "="
		for _, candidate := range []string{"<=", ">=", "<", ">", "="} {
			if strings.HasPrefix(token, candidate) {
				op = candidate
				token = strings.TrimPrefix(token, candidate)
				break
			}
		}
		mm, mn, mp, ok := parseVersion(token)
		if !ok {
			return false
		}
		left, right := [3]int{hm, hn, hp}, [3]int{mm, mn, mp}
		cmp := 0
		for i := range left {
			if left[i] < right[i] {
				cmp = -1
				break
			}
			if left[i] > right[i] {
				cmp = 1
				break
			}
		}
		switch op {
		case "<":
			if !(cmp < 0) {
				return false
			}
		case "<=":
			if !(cmp <= 0) {
				return false
			}
		case ">":
			if !(cmp > 0) {
				return false
			}
		case ">=":
			if !(cmp >= 0) {
				return false
			}
		case "=":
			if !(cmp == 0) {
				return false
			}
		}
	}
	return true
}

// LoadManifest loads plugin.yaml, plugin.yml, or plugin.json from a package
// directory. JSON is accepted so a package can be generated without YAML.
func LoadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest: %w", err)
	}
	var manifest Manifest
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		err = json.Unmarshal(data, &manifest)
	default:
		err = yaml.Unmarshal(data, &manifest)
	}
	if err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	if err := manifest.Validate(""); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}
