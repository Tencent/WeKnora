// milvus-migrate 将旧版 Milvus Collection 复制为支持中英文 BM25 的新 Collection。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"

	milvusRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/milvus"
	client "github.com/milvus-io/milvus/client/v2/milvusclient"
)

func main() {
	sourceDefault := strings.TrimSpace(os.Getenv("MILVUS_COLLECTION"))
	if sourceDefault == "" {
		sourceDefault = "weknora_embeddings"
	}
	targetDefault := sourceDefault + "_multilingual"

	source := flag.String("source", sourceDefault, "旧 Collection 前缀，例如 weknora_embeddings")
	target := flag.String("target", targetDefault, "新 Collection 前缀，例如 weknora_embeddings_multilingual")
	address := flag.String("address", envOr("MILVUS_ADDRESS", "localhost:19530"), "Milvus 地址")
	username := flag.String("username", os.Getenv("MILVUS_USERNAME"), "Milvus 用户名")
	password := flag.String("password", os.Getenv("MILVUS_PASSWORD"), "Milvus 密码")
	database := flag.String("database", os.Getenv("MILVUS_DB_NAME"), "Milvus 数据库名")
	metric := flag.String(
		"metric-type",
		strings.TrimSpace(os.Getenv("MILVUS_METRIC_TYPE")),
		"稠密向量距离：IP、COSINE 或 L2；省略则沿用源 Collection",
	)
	batchSize := flag.Int("batch-size", 64, "每批迁移的行数；文本较长时可进一步调小")
	flag.Parse()

	metricType, err := milvusRepo.ParseMetricType(*metric)
	if err != nil {
		log.Fatal(err)
	}
	if strings.TrimSpace(*source) == strings.TrimSpace(*target) {
		log.Fatal("--source 和 --target 必须不同；迁移只会复制到新 Collection，不会覆盖旧 Collection")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	connectCtx, cancelConnect := context.WithTimeout(ctx, 30*time.Second)
	milvusClient, err := client.New(connectCtx, &client.ClientConfig{
		Address:  *address,
		Username: *username,
		Password: *password,
		DBName:   *database,
	})
	cancelConnect()
	if err != nil {
		log.Fatalf("连接 Milvus 失败：%v", err)
	}
	defer func() {
		closeCtx, cancelClose := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelClose()
		if err := milvusClient.Close(closeCtx); err != nil {
			log.Printf("关闭 Milvus 连接失败：%v", err)
		}
	}()

	summary, err := milvusRepo.MigrateLegacyCollections(ctx, milvusClient, milvusRepo.MultilingualMigrationOptions{
		SourceCollectionBaseName: *source,
		TargetCollectionBaseName: *target,
		MetricType:               metricType,
		BatchSize:                *batchSize,
	})
	if err != nil {
		log.Fatalf(
			"迁移失败（已检查 %d 个 Collection，已复制 %d 个 Collection、%d 行）：%v",
			summary.ExaminedCollections,
			summary.MigratedCollections,
			summary.MigratedRows,
			err,
		)
	}
	fmt.Printf(
		"迁移完成：检查 %d 个 Collection，复制 %d 个 Collection、%d 行。旧 Collection 保留未删除。\n",
		summary.ExaminedCollections,
		summary.MigratedCollections,
		summary.MigratedRows,
	)
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
