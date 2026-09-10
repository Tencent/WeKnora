package knowledge

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// relation-contract.json is generated from the V2 Skill source by
// scripts/sync-extract-video-knowledge-v2.sh.
//
//go:embed relation-contract.json
var relationContractJSON []byte

type relationContractDocument struct {
	Version       string                                       `json:"version"`
	RelationTypes []string                                     `json:"relation_types"`
	Matrix        map[KnowledgeType]map[KnowledgeType][]string `json:"matrix"`
}

var formalRelationContract = mustLoadFormalRelationContract(relationContractJSON)

func mustLoadFormalRelationContract(data []byte) relationContractDocument {
	var contract relationContractDocument
	if err := json.Unmarshal(data, &contract); err != nil {
		panic(fmt.Sprintf("decode formal relation contract: %v", err))
	}
	if strings.TrimSpace(contract.Version) == "" || len(contract.RelationTypes) == 0 {
		panic("formal relation contract requires version and relation_types")
	}
	seen := make(map[string]struct{}, len(contract.RelationTypes))
	for _, relationType := range contract.RelationTypes {
		relationType = strings.TrimSpace(relationType)
		if relationType == "" {
			panic("formal relation contract contains an empty relation type")
		}
		if _, duplicate := seen[relationType]; duplicate {
			panic("formal relation contract contains duplicate relation type " + relationType)
		}
		seen[relationType] = struct{}{}
	}
	for source, targets := range contract.Matrix {
		if !IsKnowledgeType(source) {
			panic("formal relation contract contains unknown source type " + string(source))
		}
		for target, relationTypes := range targets {
			if !IsKnowledgeType(target) {
				panic("formal relation contract contains unknown target type " + string(target))
			}
			for _, relationType := range relationTypes {
				if _, ok := seen[relationType]; !ok {
					panic("formal relation matrix contains undeclared relation type " + relationType)
				}
			}
		}
	}
	return contract
}

func FormalRelationTypes() []string {
	result := append([]string(nil), formalRelationContract.RelationTypes...)
	sort.Strings(result)
	return result
}

func IsFormalRelationType(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, relationType := range formalRelationContract.RelationTypes {
		if value == relationType {
			return true
		}
	}
	return false
}

func IsRelationAllowedForTypes(relationType string, source, target KnowledgeType) bool {
	relationType = strings.ToLower(strings.TrimSpace(relationType))
	for _, allowed := range formalRelationContract.Matrix[source][target] {
		if allowed == relationType {
			return true
		}
	}
	return false
}

// NormalizeFormalRelationType keeps the V2 contract closed while providing
// the one unambiguous compatibility mapping for historical case relations.
func NormalizeFormalRelationType(value string, source, target KnowledgeType) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if IsFormalRelationType(value) {
		return value, true
	}
	if value == "derived_from" && source == TypeCase && target == TypeInsight {
		return "supports", true
	}
	return value, false
}
