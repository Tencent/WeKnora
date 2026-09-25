package builtin

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/plugin/configschema"
	"github.com/Tencent/WeKnora/internal/types"
)

// backendSchemaKeysFile lists the frontend locale keys the backend names:
// x-i18n-keys in builtin config schemas, IM platform link titles and mode
// hints. The frontend i18n audit treats them as used, so pruning
// never drops a label only the backend references.
const backendSchemaKeysFile = "../../../frontend/src/i18n/backendSchemaKeys.ts"

func collectI18nKeys(s *configschema.Schema, into map[string]bool) {
	if s == nil {
		return
	}
	for _, key := range s.I18nKeys {
		into[key] = true
	}
	for _, p := range s.Properties {
		collectI18nKeys(p, into)
	}
	for _, c := range s.OneOf {
		collectI18nKeys(c, into)
	}
	collectI18nKeys(s.Items, into)
}

func TestBackendSchemaKeysAreListedForTheFrontend(t *testing.T) {
	used := map[string]bool{}
	for _, info := range types.GetWebSearchProviderTypes() {
		collectI18nKeys(info.ConfigSchema, used)
	}
	for _, meta := range datasource.ListAvailableConnectors() {
		collectI18nKeys(meta.ConfigSchema, used)
	}
	for _, info := range im.KnownPlatformInfos() {
		collectI18nKeys(info.ConfigSchema, used)
		for _, link := range info.Links {
			if link.TitleKey != "" {
				used[link.TitleKey] = true
			}
		}
		if info.ModeHintKey != "" {
			used[info.ModeHintKey] = true
		}
	}

	data, err := os.ReadFile(filepath.FromSlash(backendSchemaKeysFile))
	if err != nil {
		t.Fatalf("read %s: %v", backendSchemaKeysFile, err)
	}
	listed := map[string]bool{}
	for _, m := range regexp.MustCompile(`'([A-Za-z0-9_.]+)'`).FindAllStringSubmatch(string(data), -1) {
		listed[m[1]] = true
	}

	var missing, stale []string
	for k := range used {
		if !listed[k] {
			missing = append(missing, k)
		}
	}
	for k := range listed {
		if !used[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 {
		t.Errorf("add to %s: %v", backendSchemaKeysFile, missing)
	}
	if len(stale) > 0 {
		t.Errorf("no builtin schema uses these any more, drop them from %s: %v", backendSchemaKeysFile, stale)
	}
	if !slices.IsSorted(keysInFileOrder(string(data))) {
		t.Errorf("keep %s sorted", backendSchemaKeysFile)
	}
}

func keysInFileOrder(src string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`'([A-Za-z0-9_.]+)'`).FindAllStringSubmatch(src, -1) {
		out = append(out, m[1])
	}
	return out
}
