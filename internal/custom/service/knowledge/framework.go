package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const (
	knowledgeFrameworkRelativePath  = "skill/extract-video-knowledge/references/type-frameworks.md"
	knowledgeFrameworkPreloadedPath = "skills/preloaded/extract-video-knowledge/references/type-frameworks.md"
	knowledgeFrameworkEnv           = "WEKNORA_KNOWLEDGE_FRAMEWORK_PATH"
)

var (
	frameworkTopTypePattern = regexp.MustCompile(`^(.+?)\s+` + "`" + `([a-z_]+)` + "`$")
	frameworkHeadingPattern = regexp.MustCompile("^###\\s+`([a-z_]+)`\\s+(.+?)\\s*$")
	frameworkFieldPattern   = regexp.MustCompile("^`([a-z_]+)`$")
)

// FrameworkField is a source-defined structure field and its user-facing
// Chinese label. The slice order is the page rendering order.
type FrameworkField struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// FrameworkEntry describes one primary type or one entity subtype.
type FrameworkEntry struct {
	PrimaryType   KnowledgeType    `json:"primary_type"`
	EntitySubType string           `json:"entity_sub_type,omitempty"`
	Label         string           `json:"label"`
	Fields        []FrameworkField `json:"fields"`
}

// FrameworkType is the user-facing label for one of the five primary types.
type FrameworkType struct {
	PrimaryType KnowledgeType `json:"primary_type"`
	Label       string        `json:"label"`
}

// FrameworkDigest is the runtime projection of type-frameworks.md. Its hash
// makes the source rule version explicit in acceptance artifacts and logs.
type FrameworkDigest struct {
	SourcePath   string           `json:"source_path"`
	SourceSHA256 string           `json:"source_sha256"`
	Types        []FrameworkType  `json:"types"`
	Entries      []FrameworkEntry `json:"entries"`
}

var defaultFramework = struct {
	sync.Once
	digest FrameworkDigest
	err    error
}{}

// LoadDefaultFrameworkDigest loads the canonical framework source once.
// WEKNORA_KNOWLEDGE_FRAMEWORK_PATH can point to the deployed copy.
func LoadDefaultFrameworkDigest() (FrameworkDigest, error) {
	defaultFramework.Do(func() {
		path, err := resolveFrameworkPath()
		if err != nil {
			defaultFramework.err = err
			return
		}
		defaultFramework.digest, defaultFramework.err = LoadFrameworkDigest(path)
	})
	return cloneFrameworkDigest(defaultFramework.digest), defaultFramework.err
}

// LoadFrameworkDigest reads and validates one framework source file.
func LoadFrameworkDigest(path string) (FrameworkDigest, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return FrameworkDigest{}, fmt.Errorf("resolve knowledge framework path: %w", err)
	}
	data, err := os.ReadFile(absolutePath)
	if err != nil {
		return FrameworkDigest{}, fmt.Errorf("read knowledge framework %q: %w", absolutePath, err)
	}
	digest, err := parseFrameworkSource(data, absolutePath)
	if err != nil {
		return FrameworkDigest{}, err
	}
	return digest, nil
}

// FrameworkFor returns the source-defined entry for a type/subtype pair.
func FrameworkFor(primaryType KnowledgeType, entitySubType string) (FrameworkEntry, error) {
	digest, err := LoadDefaultFrameworkDigest()
	if err != nil {
		return FrameworkEntry{}, err
	}
	for _, entry := range digest.Entries {
		if entry.PrimaryType == primaryType && strings.TrimSpace(entry.EntitySubType) == strings.TrimSpace(entitySubType) {
			return cloneFrameworkEntry(entry), nil
		}
	}
	if strings.TrimSpace(entitySubType) == "" {
		for _, entry := range digest.Types {
			if entry.PrimaryType == primaryType {
				return FrameworkEntry{PrimaryType: entry.PrimaryType, Label: entry.Label}, nil
			}
		}
	}
	return FrameworkEntry{}, fmt.Errorf("no framework for primary_type %q and entity_sub_type %q", primaryType, entitySubType)
}

// frameworkKeys is the compatibility helper used by the existing knowledge
// pipeline. The returned order comes from type-frameworks.md.
func frameworkKeys(primaryType KnowledgeType, entitySubType string) []string {
	entry, err := FrameworkFor(primaryType, entitySubType)
	if err != nil {
		return nil
	}
	keys := make([]string, 0, len(entry.Fields))
	for _, field := range entry.Fields {
		keys = append(keys, field.Key)
	}
	return keys
}

func frameworkLabel(primaryType KnowledgeType, entitySubType string) string {
	entry, err := FrameworkFor(primaryType, entitySubType)
	if err != nil {
		return ""
	}
	return entry.Label
}

func frameworkClassificationRules() []classificationRule {
	digest, err := LoadDefaultFrameworkDigest()
	if err != nil {
		return nil
	}
	rules := make([]classificationRule, 0, len(digest.Entries))
	for index, entry := range digest.Entries {
		keys := make([]string, 0, len(entry.Fields))
		for _, field := range entry.Fields {
			keys = append(keys, field.Key)
		}
		rules = append(rules, classificationRule{
			primaryType:   entry.PrimaryType,
			entitySubType: entry.EntitySubType,
			fields:        keys,
			priority:      index,
		})
	}
	return rules
}

func resolveFrameworkPath() (string, error) {
	if path := strings.TrimSpace(os.Getenv(knowledgeFrameworkEnv)); path != "" {
		return path, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve knowledge framework from working directory: %w", err)
	}
	for dir := filepath.Clean(cwd); ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, knowledgeFrameworkRelativePath)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return resolveBundledFrameworkPathFrom(cwd)
}

func resolveBundledFrameworkPath() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve bundled knowledge framework from working directory: %w", err)
	}
	return resolveBundledFrameworkPathFrom(cwd)
}

func resolveBundledFrameworkPathFrom(start string) (string, error) {
	for dir := filepath.Clean(start); ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, knowledgeFrameworkPreloadedPath)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return "", fmt.Errorf("bundled knowledge framework source not found")
}

func parseFrameworkSource(data []byte, sourcePath string) (FrameworkDigest, error) {
	sourceHash := sha256.Sum256(data)
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	topLabels := make(map[KnowledgeType]string)
	displayLabels := make(map[string][]string)
	detailFields := make(map[string][]FrameworkField)
	detailLabels := make(map[string]string)
	currentDetailKey := ""
	inStructureMapping := false

	for _, rawLine := range lines {
		line := strings.TrimSpace(strings.TrimPrefix(rawLine, "\ufeff"))
		if strings.HasPrefix(line, "## ") {
			sectionTitle := strings.TrimSpace(strings.TrimPrefix(line, "## "))
			inStructureMapping = sectionTitle == "结构维度展示契约"
			currentDetailKey = ""
			switch {
			case strings.Contains(sectionTitle, "方法论结构维度"):
				currentDetailKey = string(TypeMethodology)
			case strings.Contains(sectionTitle, "案例结构维度"):
				currentDetailKey = string(TypeCase)
			case strings.Contains(sectionTitle, "概念结构维度"):
				currentDetailKey = string(TypeConcept)
			case strings.Contains(sectionTitle, "洞察结构维度"):
				currentDetailKey = string(TypeInsight)
			}
			continue
		}
		if matches := frameworkHeadingPattern.FindStringSubmatch(line); len(matches) == 3 {
			currentDetailKey = matches[1]
			detailLabels[currentDetailKey] = strings.TrimSpace(matches[2])
			inStructureMapping = false
			continue
		}
		cells := markdownTableCells(line)
		if len(cells) < 2 {
			continue
		}
		if inStructureMapping {
			key := strings.Trim(cells[0], "` ")
			if key == "" || strings.Contains(key, "/") || strings.EqualFold(key, "primary_type") {
				continue
			}
			displayLabels[key] = splitFrameworkLabels(cells[1])
			continue
		}
		if currentDetailKey == "" {
			if len(cells) >= 3 {
				if match := frameworkTopTypePattern.FindStringSubmatch(cells[0]); len(match) == 3 {
					primaryType := KnowledgeType(match[2])
					topLabels[primaryType] = strings.TrimSpace(match[1])
				}
			}
			continue
		}
		if len(cells) < 3 || !frameworkFieldPattern.MatchString(cells[0]) {
			continue
		}
		match := frameworkFieldPattern.FindStringSubmatch(cells[0])
		detailFields[currentDetailKey] = append(detailFields[currentDetailKey], FrameworkField{
			Key:   match[1],
			Label: strings.TrimSpace(cells[1]),
		})
	}

	entries := make([]FrameworkEntry, 0, 11)
	for _, primaryType := range []KnowledgeType{TypeEntity, TypeMethodology, TypeCase, TypeConcept, TypeInsight} {
		if primaryType == TypeEntity {
			for _, subtype := range []string{"person", "organization", "product", "technology", "industry", "place"} {
				entry, err := buildFrameworkEntry(primaryType, subtype, detailFields, detailLabels, displayLabels)
				if err != nil {
					return FrameworkDigest{}, err
				}
				entries = append(entries, entry)
			}
			continue
		}
		entry, err := buildFrameworkEntry(primaryType, "", detailFields, detailLabels, displayLabels)
		if err != nil {
			return FrameworkDigest{}, err
		}
		if strings.TrimSpace(topLabels[primaryType]) == "" {
			return FrameworkDigest{}, fmt.Errorf("framework type %q has no Chinese label", primaryType)
		}
		entry.Label = topLabels[primaryType]
		entries = append(entries, entry)
	}

	if len(topLabels) != 5 {
		return FrameworkDigest{}, fmt.Errorf("framework must define exactly five primary types, got %d", len(topLabels))
	}
	types := make([]FrameworkType, 0, 5)
	for _, primaryType := range []KnowledgeType{TypeEntity, TypeMethodology, TypeCase, TypeConcept, TypeInsight} {
		label := strings.TrimSpace(topLabels[primaryType])
		if label == "" {
			return FrameworkDigest{}, fmt.Errorf("framework type %q has no Chinese label", primaryType)
		}
		types = append(types, FrameworkType{PrimaryType: primaryType, Label: label})
	}
	return FrameworkDigest{
		SourcePath:   sourcePath,
		SourceSHA256: hex.EncodeToString(sourceHash[:]),
		Types:        types,
		Entries:      entries,
	}, nil
}

func buildFrameworkEntry(primaryType KnowledgeType, entitySubType string, detailFields map[string][]FrameworkField, detailLabels map[string]string, displayLabels map[string][]string) (FrameworkEntry, error) {
	detailKey := string(primaryType)
	if primaryType == TypeEntity {
		detailKey = entitySubType
	}
	fields := detailFields[detailKey]
	if len(fields) == 0 {
		return FrameworkEntry{}, fmt.Errorf("framework %q has no structure fields", detailKey)
	}
	labels := displayLabels[detailKey]
	if len(labels) != len(fields) {
		return FrameworkEntry{}, fmt.Errorf("framework %q display field count %d does not match detail field count %d", detailKey, len(labels), len(fields))
	}
	result := FrameworkEntry{
		PrimaryType:   primaryType,
		EntitySubType: entitySubType,
		Label:         detailLabels[detailKey],
		Fields:        make([]FrameworkField, len(fields)),
	}
	for index, field := range fields {
		result.Fields[index] = FrameworkField{Key: field.Key, Label: labels[index]}
	}
	return result, nil
}

func markdownTableCells(line string) []string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
		return nil
	}
	parts := strings.Split(line[1:len(line)-1], "|")
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
	}
	return parts
}

func splitFrameworkLabels(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == '、' || r == ',' || r == '，'
	})
	labels := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			labels = append(labels, trimmed)
		}
	}
	return labels
}

func cloneFrameworkDigest(value FrameworkDigest) FrameworkDigest {
	cloned := value
	cloned.Types = append([]FrameworkType(nil), value.Types...)
	cloned.Entries = make([]FrameworkEntry, len(value.Entries))
	for index, entry := range value.Entries {
		cloned.Entries[index] = cloneFrameworkEntry(entry)
	}
	return cloned
}

func cloneFrameworkEntry(value FrameworkEntry) FrameworkEntry {
	cloned := value
	cloned.Fields = append([]FrameworkField(nil), value.Fields...)
	return cloned
}
