package neo4j

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

type MemoryRepository struct {
	driver neo4j.Driver
}

func NewMemoryRepository(driver neo4j.Driver) interfaces.MemoryRepository {
	return &MemoryRepository{driver: driver}
}

func (r *MemoryRepository) IsAvailable(ctx context.Context) bool {
	return r.driver != nil
}

func (r *MemoryRepository) SaveEpisode(ctx context.Context, episode *types.Episode, entities []*types.Entity, relations []*types.Relationship) error {
	session := r.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		// 1. Create Episode Node
		createEpisodeQuery := `
			MERGE (e:Episode {id: $id})
			SET e.user_id = $user_id,
				e.session_id = $session_id,
				e.summary = $summary,
				e.created_at = $created_at
		`
		_, err := tx.Run(ctx, createEpisodeQuery, map[string]interface{}{
			"id":         episode.ID,
			"user_id":    episode.UserID,
			"session_id": episode.SessionID,
			"summary":    episode.Summary,
			"created_at": episode.CreatedAt.Format(time.RFC3339),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create episode: %v", err)
		}

		// 2. Create Entity Nodes and MENTIONS relationships
		for _, entity := range entities {
			createEntityQuery := `
				MERGE (n:Entity {name: $name})
				SET n.type = $type,
					n.description = $description
				WITH n
				MATCH (e:Episode {id: $episode_id})
				MERGE (e)-[:MENTIONS]->(n)
			`
			_, err := tx.Run(ctx, createEntityQuery, map[string]interface{}{
				"name":        entity.Title,
				"type":        entity.Type,
				"description": entity.Description,
				"episode_id":  episode.ID,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create entity %s: %v", entity.Title, err)
			}
		}

		// 3. Create Relationships between Entities
		for _, rel := range relations {
			createRelQuery := `
				MATCH (s:Entity {name: $source})
				MATCH (t:Entity {name: $target})
				MERGE (s)-[r:RELATED_TO {description: $description}]->(t)
				SET r.weight = $weight
			`
			_, err := tx.Run(ctx, createRelQuery, map[string]interface{}{
				"source":      rel.Source,
				"target":      rel.Target,
				"description": rel.Description,
				"weight":      rel.Weight,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create relationship between %s and %s: %v", rel.Source, rel.Target, err)
			}
		}

		return nil, nil
	})

	if err != nil {
		logger.Errorf(ctx, "failed to save episode: %v", err)
		return err
	}

	return nil
}

func (r *MemoryRepository) FindRelatedEpisodes(ctx context.Context, userID string, keywords []string, limit int) ([]*types.Episode, error) {
	session := r.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	result, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		querySimple := `
			MATCH (e:Episode)-[:MENTIONS]->(n:Entity)
			WHERE e.user_id = $user_id AND n.name IN $keywords
			RETURN DISTINCT e
			ORDER BY e.created_at DESC
			LIMIT $limit
		`

		res, err := tx.Run(ctx, querySimple, map[string]interface{}{
			"user_id":  userID,
			"keywords": keywords,
			"limit":    limit,
		})
		if err != nil {
			return nil, err
		}

		var episodes []*types.Episode
		for res.Next(ctx) {
			record := res.Record()
			node, _ := record.Get("e")
			episodeNode := node.(neo4j.Node)

			createdAtStr := episodeNode.Props["created_at"].(string)
			createdAt, _ := time.Parse(time.RFC3339, createdAtStr)

			episodes = append(episodes, &types.Episode{
				ID:        episodeNode.Props["id"].(string),
				UserID:    episodeNode.Props["user_id"].(string),
				SessionID: episodeNode.Props["session_id"].(string),
				Summary:   episodeNode.Props["summary"].(string),
				CreatedAt: createdAt,
			})
		}
		return episodes, nil
	})

	if err != nil {
		return nil, err
	}

	return result.([]*types.Episode), nil
}
