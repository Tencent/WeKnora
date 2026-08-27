// Package main 是自研后端（custom-backend）的服务入口。
// 独立于 WeKnora 官方 server，提供视频产品的业务 API，
// 与官方 cmd/server 分离部署（见个性化部署流程 §2.5）。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/client/minio"
	"github.com/Tencent/WeKnora/internal/custom/client/tongyi"
	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/handler"
	"github.com/Tencent/WeKnora/internal/custom/migrations"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/skill"
	"github.com/Tencent/WeKnora/internal/custom/worker"
)

func main() {
	cfg := config.Load()
	initLogger()

	db, err := openDatabase(cfg)
	if err != nil {
		slog.Error("connect database failed", "error", err)
		os.Exit(1)
	}

	if cfg.Database.Driver == "postgres" {
		if err := runMigrations(cfg); err != nil {
			slog.Error("run migrations failed", "error", err)
			os.Exit(1)
		}
	} else {
		if err := autoMigrateLocalDB(db); err != nil {
			slog.Error("auto migrate local db failed", "error", err)
			os.Exit(1)
		}
	}

	// 初始化外部依赖客户端（任一失败不阻塞启动，job 运行时再感知）
	minioCli, _ := minio.New(cfg.MinIO)
	if minioCli != nil {
		if err := minioCli.EnsureBucket(context.Background()); err != nil {
			slog.Error("ensure minio bucket failed", "error", err)
			os.Exit(1)
		}
	}
	weknoraCli := weknora.New(cfg.WeKnora)
	tongyiCli := tongyi.New(cfg.Tongyi)
	wikiClient := weknora.NewWikiClient(cfg.WeKnora)
	agentClient := weknora.NewAgentClient(cfg.WeKnora)
	kbClient := weknora.NewKBClient(cfg.WeKnora)

	// CP-T010：启动时一次性开启原生 Wiki 抽取（失败仅 warn，不阻塞）
	if cfg.WeKnora.KBID != "" {
		if err := kbClient.EnableWikiExtraction(context.Background(), cfg.WeKnora.KBID); err != nil {
			slog.Warn("enable wiki extraction", "error", err)
		}
	}

	// 内容生产 skill 编排器（CP-T005 / CP-T006）
	orchestrator := skill.NewOrchestrator(db, wikiClient, cfg.WeKnora.KBID)

	skipWorkers := os.Getenv("CUSTOM_DISABLE_WORKERS") == "true" || cfg.Database.Driver == "sqlite"
	var engine *worker.Engine
	if skipWorkers {
		slog.Info("custom workers disabled")
	} else {
		contentAgentID := os.Getenv("CUSTOM_CONTENT_AGENT_ID")
		missingContentConfig := make([]string, 0, 4)
		if contentAgentID == "" {
			missingContentConfig = append(missingContentConfig, "CUSTOM_CONTENT_AGENT_ID")
		}
		if cfg.Tongyi.AccessKeyID == "" {
			missingContentConfig = append(missingContentConfig, "TONGYI_ACCESS_KEY_ID")
		}
		if cfg.Tongyi.AccessKeySecret == "" {
			missingContentConfig = append(missingContentConfig, "TONGYI_ACCESS_KEY_SECRET")
		}
		if cfg.Tongyi.AppKey == "" {
			missingContentConfig = append(missingContentConfig, "TONGYI_APP_KEY")
		}
		if cfg.WeKnora.KBID == "" {
			missingContentConfig = append(missingContentConfig, "WEKNORA_KB_ID")
		}

		contentWorkersEnabled := len(missingContentConfig) == 0
		if !contentWorkersEnabled {
			slog.Warn("content workers disabled; thumbnail worker remains enabled", "missing_config", missingContentConfig)
		}

		base := worker.BaseSkillHandler{
			DB:           db,
			AgentClient:  agentClient,
			Orchestrator: orchestrator,
			AgentID:      contentAgentID,
		}

		handlers := []worker.Handler{
			worker.NewThumbnailHandler(db, minioCli, contentWorkersEnabled),
		}
		if contentWorkersEnabled {
			handlers = append(handlers,
				worker.NewTranscriptionHandler(db, tongyiCli, cfg.Tongyi.InternalFrontendBaseURL),
				worker.NewSubtitleGenerateHandler(db, minioCli, tongyiCli),
				worker.NewIndexHandler(db, weknoraCli, orchestrator),
				&worker.GraphHandler{BaseSkillHandler: base},
				&worker.OutlineHandler{BaseSkillHandler: base},
				&worker.OverviewHandler{BaseSkillHandler: base},
				&worker.SummaryHandler{BaseSkillHandler: base},
				&worker.AssembleHandler{BaseSkillHandler: base},
			)
		}
		engine = worker.NewEngine(db, &cfg.Worker, handlers...)
		engine.Start(context.Background())
		defer engine.Stop()
	}

	// HTTP 服务
	router := handler.NewRouter(db, cfg)
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	srv := &http.Server{Addr: addr, Handler: router}

	go func() {
		slog.Info("custom-backend starting", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("forced shutdown", "error", err)
	}
	slog.Info("server exited")
}

func initLogger() {
	level := slog.LevelInfo
	if os.Getenv("CUSTOM_LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})))
}

func openDatabase(cfg *config.Config) (*gorm.DB, error) {
	if cfg.Database.Driver == "sqlite" {
		if err := os.MkdirAll(filepath.Dir(cfg.Database.Path), 0o755); err != nil {
			return nil, err
		}
		return gorm.Open(sqlite.Open(cfg.Database.Path), &gorm.Config{})
	}
	return gorm.Open(postgres.Open(cfg.Database.DSN()), &gorm.Config{})
}

func autoMigrateLocalDB(db *gorm.DB) error {
	return db.AutoMigrate(
		&model.Video{},
		&model.VideoTranscriptChunk{},
		&model.VideoProcessingJob{},
		&model.VideoSummaryFramework{},
		&model.DashboardQuestionStat{},
		&model.DashboardQuestionCluster{},
	)
}

// runMigrations 跑内嵌的迁移 SQL（库需预先存在，由 docker compose 初始化）
func runMigrations(cfg *config.Config) error {
	d, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return err
	}
	m, err := migrate.NewWithSourceInstance("iofs", d, cfg.Database.MigrateURL())
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}
