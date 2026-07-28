package artifact

import (
	"errors"
	"fmt"
)

type EntityState struct {
	ID             string
	MatchKey       string
	ContentDigest  string
	ArtifactDigest string
	MetadataDigest string
}

type EntityPair struct {
	Desired EntityState
	Live    EntityState
}

type ReconcilePlan struct {
	Kept         []EntityPair
	MetadataOnly []EntityPair
	Added        []EntityState
	Changed      []EntityPair
	Stale        []EntityState
}

// PlanDesiredState computes a non-destructive diff. Callers materialize Added
// and Changed first, publish only while fenced, then clean exactly Stale last.
func PlanDesiredState(desired, live []EntityState) (ReconcilePlan, error) {
	plan := ReconcilePlan{}
	liveByMatch := make(map[string]EntityState, len(live))
	for _, entity := range live {
		if err := validateEntityState(entity); err != nil {
			return ReconcilePlan{}, fmt.Errorf("invalid live entity: %w", err)
		}
		if _, exists := liveByMatch[entity.MatchKey]; exists {
			return ReconcilePlan{}, fmt.Errorf("duplicate live entity match key %q", entity.MatchKey)
		}
		liveByMatch[entity.MatchKey] = entity
	}

	desiredMatches := make(map[string]struct{}, len(desired))
	for _, entity := range desired {
		if err := validateEntityState(entity); err != nil {
			return ReconcilePlan{}, fmt.Errorf("invalid desired entity: %w", err)
		}
		if _, exists := desiredMatches[entity.MatchKey]; exists {
			return ReconcilePlan{}, fmt.Errorf("duplicate desired entity match key %q", entity.MatchKey)
		}
		desiredMatches[entity.MatchKey] = struct{}{}

		current, found := liveByMatch[entity.MatchKey]
		if !found {
			plan.Added = append(plan.Added, entity)
			continue
		}
		pair := EntityPair{Desired: entity, Live: current}
		switch {
		case entity.ID != current.ID ||
			entity.ContentDigest != current.ContentDigest ||
			entity.ArtifactDigest != current.ArtifactDigest:
			plan.Changed = append(plan.Changed, pair)
		case entity.MetadataDigest != current.MetadataDigest:
			plan.MetadataOnly = append(plan.MetadataOnly, pair)
		default:
			plan.Kept = append(plan.Kept, pair)
		}
	}

	for _, entity := range live {
		if _, desired := desiredMatches[entity.MatchKey]; !desired {
			plan.Stale = append(plan.Stale, entity)
		}
	}
	return plan, nil
}

func validateEntityState(entity EntityState) error {
	if entity.ID == "" {
		return errors.New("entity ID must not be empty")
	}
	if entity.MatchKey == "" {
		return errors.New("entity match key must not be empty")
	}
	if !isSHA256(entity.ContentDigest) {
		return errors.New("entity content digest must be a SHA-256")
	}
	if entity.ArtifactDigest != "" && !isSHA256(entity.ArtifactDigest) {
		return errors.New("entity artifact digest must be empty or a SHA-256")
	}
	if entity.MetadataDigest != "" && !isSHA256(entity.MetadataDigest) {
		return errors.New("entity metadata digest must be empty or a SHA-256")
	}
	return nil
}
