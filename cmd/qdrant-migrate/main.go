// qdrant-migrate copies WeKnora's Qdrant collections into a BM25-enabled prefix.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	qdrantRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/qdrant"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/qdrant/go-client/qdrant"
)

func main() {
	store := types.FindEnvVectorStore("qdrant", os.Getenv, "__env_qdrant__")
	cc := store.ConnectionConfig
	source := flag.String("source", store.IndexConfig.GetIndexNameOrDefault(types.QdrantRetrieverEngineType),
		"Source collection prefix (stop writers before migrating)")
	target := flag.String("target", "", "New destination prefix; required")
	host := flag.String("host", cc.Host, "Qdrant host")
	port := flag.Int("port", cc.Port, "Qdrant gRPC port")
	tls := flag.Bool("tls", cc.UseTLS, "Use TLS")
	resume := flag.Bool("resume", false, "Resume this migration into its existing destination")
	timeout := flag.Duration("timeout", time.Hour, "Maximum migration duration")
	flag.Parse()
	if *target == "" || *source == *target || *timeout <= 0 {
		log.Fatal("provide a different --target prefix and a positive --timeout")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	client, err := qdrant.NewClient(&qdrant.Config{
		Host: *host, Port: *port, APIKey: cc.APIKey, UseTLS: *tls,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			log.Printf("close Qdrant client: %v", err)
		}
	}()
	copied, err := qdrantRepo.MigrateBM25(ctx, client, *source, *target, *resume)
	if err != nil {
		log.Fatalf("migration stopped after %d copied points: %v", copied, err)
	}
	fmt.Printf("Copied %d points. Source is unchanged. Keep writers stopped until cutover.\n", copied)
	fmt.Printf("Set QDRANT_COLLECTION=%s and QDRANT_KEYWORD_SEARCH=bm25, then restart all app instances.\n", *target)
}
