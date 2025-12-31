package client

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// ExampleUsage demonstrates the complete usage flow of the WeKnora client, including:
// - Creating a client instance
// - Creating a knowledge base
// - Uploading knowledge files
// - Creating a session
// - Performing question-answering
// - Using streaming question-answering
// - Managing models
// - Managing knowledge chunks
// - Getting session messages
// - Cleaning up resources
func ExampleUsage() {
	// Create a client instance
	apiClient := NewClient(
		"http://localhost:8080",
		WithToken("your-auth-token"),
		WithTimeout(30*time.Second),
	)

	// 1. Create a knowledge base
	fmt.Println("1. Creating knowledge base...")
	kb := &KnowledgeBase{
		Name:        "Test Knowledge Base",
		Description: "This is a test knowledge base",
		ChunkingConfig: ChunkingConfig{
			ChunkSize:    500,
			ChunkOverlap: 50,
			Separators:   []string{"\n\n", "\n", ". ", "? ", "! "},
		},
		ImageProcessingConfig: ImageProcessingConfig{
			ModelID: "image_model_id",
		},
		EmbeddingModelID: "embedding_model_id",
		SummaryModelID:   "summary_model_id",
	}

	createdKB, err := apiClient.CreateKnowledgeBase(context.Background(), kb)
	if err != nil {
		fmt.Printf("Failed to create knowledge base: %v\n", err)
		return
	}
	fmt.Printf("Knowledge base created successfully: ID=%s, Name=%s\n", createdKB.ID, createdKB.Name)

	// 2. Upload knowledge file
	fmt.Println("\n2. Uploading knowledge file...")
	filePath := "path/to/sample.pdf" // Sample file path

	// Check if file exists before uploading
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		fmt.Printf("File does not exist: %s, skipping upload step\n", filePath)
	} else {
		// Add metadata
		metadata := map[string]string{
			"source": "local",
			"type":   "document",
		}
		knowledge, err := apiClient.CreateKnowledgeFromFile(context.Background(), createdKB.ID, filePath, metadata, nil, "")
		if err != nil {
			fmt.Printf("Failed to upload knowledge file: %v\n", err)
		} else {
			fmt.Printf("File uploaded successfully: Knowledge ID=%s, Title=%s\n", knowledge.ID, knowledge.Title)
		}
	}

	// Create text knowledge (alternative to file upload)
	// Note: This is just an example, the client package may not support creating text knowledge directly
	// In actual use, refer to the methods provided in client.knowledge.go
	fmt.Println("\nCreating text knowledge (example)")
	fmt.Println("Title: Test Text Knowledge")
	fmt.Println("Description: Test knowledge created from text")

	// 3. Create a model
	fmt.Println("\n3. Creating model...")
	modelRequest := &CreateModelRequest{
		Name:        "Test Model",
		Type:        ModelTypeChat,
		Source:      ModelSourceInternal,
		Description: "This is a test model",
		Parameters: ModelParameters{
			"temperature": 0.7,
			"top_p":       0.9,
		},
		IsDefault: true,
	}

	model, err := apiClient.CreateModel(context.Background(), modelRequest)
	if err != nil {
		fmt.Printf("Failed to create model: %v\n", err)
	} else {
		fmt.Printf("Model created successfully: ID=%s, Name=%s\n", model.ID, model.Name)
	}

	// List all models
	models, err := apiClient.ListModels(context.Background())
	if err != nil {
		fmt.Printf("Failed to get model list: %v\n", err)
	} else {
		fmt.Printf("System has %d models\n", len(models))
	}

	// 4. Create a session
	fmt.Println("\n4. Creating session...")
	sessionRequest := &CreateSessionRequest{
		Title:       "Test Session",
		Description: "A test session for knowledge Q&A",
	}

	session, err := apiClient.CreateSession(context.Background(), sessionRequest)
	if err != nil {
		fmt.Printf("Failed to create session: %v\n", err)
		return
	}
	fmt.Printf("Session created successfully: ID=%s\n", session.ID)

	// 5. Perform knowledge Q&A (using streaming API)
	fmt.Println("\n5. Performing knowledge Q&A...")
	question := "What is artificial intelligence?"
	fmt.Printf("Question: %s\nAnswer: ", question)

	// Use streaming API for Q&A (Note: Client may only provide streaming Q&A API)
	var answer strings.Builder
	var references []*SearchResult

	err = apiClient.KnowledgeQAStream(context.Background(),
		session.ID,
		&KnowledgeQARequest{Query: question},
		func(response *StreamResponse) error {
			if response.ResponseType == ResponseTypeAnswer {
				answer.WriteString(response.Content)
			}

			if response.Done && len(response.KnowledgeReferences) > 0 {
				references = response.KnowledgeReferences
			}
			return nil
		})

	if err != nil {
		fmt.Printf("Q&A failed: %v\n", err)
	} else {
		fmt.Printf("%s\n", answer.String())
		if len(references) > 0 {
			fmt.Println("References:")
			for i, ref := range references {
				fmt.Printf("%d. %s\n", i+1, ref.Content[:min(50, len(ref.Content))]+"...")
			}
		}
	}

	// 6. Perform another streaming Q&A
	fmt.Println("\n6. Performing streaming Q&A...")
	streamQuestion := "What is machine learning?"
	fmt.Printf("Question: %s\nAnswer: ", streamQuestion)

	err = apiClient.KnowledgeQAStream(context.Background(),
		session.ID,
		&KnowledgeQARequest{Query: streamQuestion},
		func(response *StreamResponse) error {
			fmt.Print(response.Content)
			return nil
		},
	)
	if err != nil {
		fmt.Printf("\nStreaming Q&A failed: %v\n", err)
	}
	fmt.Println() // Line break

	// 7. Get session messages
	fmt.Println("\n7. Getting session messages...")
	messages, err := apiClient.GetRecentMessages(context.Background(), session.ID, 10)
	if err != nil {
		fmt.Printf("Failed to get session messages: %v\n", err)
	} else {
		fmt.Printf("Retrieved %d recent messages:\n", len(messages))
		for i, msg := range messages {
			fmt.Printf("%d. Role: %s, Content: %s\n", i+1, msg.Role, msg.Content[:min(30, len(msg.Content))]+"...")
		}
	}

	// 8. Manage knowledge chunks
	// Assume we have uploaded knowledge and have a knowledge ID
	knowledgeID := "knowledge_id_example" // In actual use, use a real knowledge ID

	fmt.Println("\n8. Managing knowledge chunks...")
	chunks, total, err := apiClient.ListKnowledgeChunks(context.Background(), knowledgeID, 1, 10)
	if err != nil {
		fmt.Printf("Failed to get knowledge chunks: %v\n", err)
	} else {
		fmt.Printf("Knowledge has %d chunks, retrieved %d chunks\n", total, len(chunks))

		if len(chunks) > 0 {
			// Update the first chunk
			chunkID := chunks[0].ID
			updateRequest := &UpdateChunkRequest{
				Content:   "Updated chunk content - " + chunks[0].Content,
				IsEnabled: true,
			}

			updatedChunk, err := apiClient.UpdateChunk(context.Background(), knowledgeID, chunkID, updateRequest)
			if err != nil {
				fmt.Printf("Failed to update chunk: %v\n", err)
			} else {
				fmt.Printf("Chunk updated successfully: ID=%s\n", updatedChunk.ID)
			}
		}
	}

	// 10. Clean up resources (optional, in actual use, keep or delete as needed)
	fmt.Println("\n10. Cleaning up resources...")
	if session != nil {
		if err := apiClient.DeleteSession(context.Background(), session.ID); err != nil {
			fmt.Printf("Failed to delete session: %v\n", err)
		} else {
			fmt.Println("Session deleted")
		}
	}

	// Delete knowledge (assuming we have a valid knowledge ID)
	if knowledgeID != "" {
		if err := apiClient.DeleteKnowledge(context.Background(), knowledgeID); err != nil {
			fmt.Printf("Failed to delete knowledge: %v\n", err)
		} else {
			fmt.Println("Knowledge deleted")
		}
	}

	if createdKB != nil {
		if err := apiClient.DeleteKnowledgeBase(context.Background(), createdKB.ID); err != nil {
			fmt.Printf("Failed to delete knowledge base: %v\n", err)
		} else {
			fmt.Println("Knowledge base deleted")
		}
	}

	fmt.Println("\nExample completed")
}

// min returns the smaller of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
