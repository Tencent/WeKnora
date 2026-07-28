package router

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/backup"
	"github.com/Tencent/WeKnora/internal/database"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

const operationsMetricNamespace = "weknora"

type operationsObserver struct {
	db             *gorm.DB
	redisClient    *redis.Client
	startedAt      time.Time
	backupManager  manualBackupCreator
	backupSchedule *backup.MySQLScheduler
	auditService   interfaces.AuditLogService

	registry              *prometheus.Registry
	metricsHandler        http.Handler
	httpRequests          *prometheus.CounterVec
	httpDuration          *prometheus.HistogramVec
	httpInFlight          prometheus.Gauge
	dependencyUp          *prometheus.GaugeVec
	dependencyConfigured  *prometheus.GaugeVec
	dbOpenConnections     prometheus.Gauge
	dbInUseConnections    prometheus.Gauge
	dbWaitCount           prometheus.Gauge
	logFileBytes          prometheus.Gauge
	diskFreeBytes         prometheus.Gauge
	schemaVersion         prometheus.Gauge
	schemaDirty           prometheus.Gauge
	buildInfo             *prometheus.GaugeVec
	backupScheduleEnabled prometheus.Gauge
	backupLastSuccess     prometheus.Gauge
	backupLastFailure     prometheus.Gauge
	backupRetentionFailed prometheus.Gauge
}

type operationsStatusResponse struct {
	Status        string                    `json:"status"`
	UptimeSeconds int64                     `json:"uptime_seconds"`
	Dependencies  map[string]string         `json:"dependencies"`
	Database      operationsDatabaseStatus  `json:"database"`
	FileLog       operationsFileLogStatus   `json:"file_log"`
	Migration     operationsMigrationStatus `json:"migration"`
	Backup        operationsBackupStatus    `json:"backup"`
}

type operationsDatabaseStatus struct {
	Driver           string `json:"driver"`
	OpenConnections  int    `json:"open_connections"`
	InUseConnections int    `json:"in_use_connections"`
	WaitCount        int64  `json:"wait_count"`
}

type operationsFileLogStatus struct {
	Enabled        bool   `json:"enabled"`
	SizeBytes      int64  `json:"size_bytes"`
	DiskFreeBytes  uint64 `json:"disk_free_bytes"`
	DiskTotalBytes uint64 `json:"disk_total_bytes"`
	DiskState      string `json:"disk_state"`
}

type operationsMigrationStatus struct {
	Known   bool `json:"known"`
	Version uint `json:"version"`
	Dirty   bool `json:"dirty"`
}

type operationsBackupStatus struct {
	Scheduled              bool             `json:"scheduled"`
	ConfigurationError     bool             `json:"configuration_error"`
	Schedule               string           `json:"schedule,omitempty"`
	RetentionDays          int              `json:"retention_days"`
	MinFreeGB              int              `json:"min_free_gb"`
	LastSuccessAt          time.Time        `json:"last_success_at,omitempty"`
	LastFailureAt          time.Time        `json:"last_failure_at,omitempty"`
	LastFailureKind        backup.ErrorKind `json:"last_failure_kind,omitempty"`
	LastRetentionAt        time.Time        `json:"last_retention_at,omitempty"`
	LastRetentionFailureAt time.Time        `json:"last_retention_failure_at,omitempty"`
	LastRetentionDeleted   int              `json:"last_retention_deleted"`
}

func newOperationsObserver(db *gorm.DB, redisClient *redis.Client) *operationsObserver {
	registry := prometheus.NewRegistry()
	backupManager := backup.NewMySQLManager(db)
	observer := &operationsObserver{
		db:             db,
		redisClient:    redisClient,
		startedAt:      time.Now(),
		backupManager:  backupManager,
		backupSchedule: backup.NewMySQLScheduler(backupManager),
		registry:       registry,
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: operationsMetricNamespace,
			Name:      "http_requests_total",
			Help:      "Total HTTP requests handled by WeKnora.",
		}, []string{"method", "status_class"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: operationsMetricNamespace,
			Name:      "http_request_duration_seconds",
			Help:      "HTTP request duration in seconds.",
		}, []string{"method", "status_class"}),
		httpInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: operationsMetricNamespace,
			Name:      "http_in_flight_requests",
			Help:      "Current HTTP requests being handled by WeKnora.",
		}),
		dependencyUp: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: operationsMetricNamespace,
			Name:      "dependency_up",
			Help:      "Whether a configured dependency is reachable.",
		}, []string{"dependency"}),
		dependencyConfigured: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: operationsMetricNamespace,
			Name:      "dependency_configured",
			Help:      "Whether a dependency is enabled for this deployment.",
		}, []string{"dependency"}),
		dbOpenConnections: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: operationsMetricNamespace,
			Name:      "db_open_connections",
			Help:      "Open database connections.",
		}),
		dbInUseConnections: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: operationsMetricNamespace,
			Name:      "db_in_use_connections",
			Help:      "Database connections currently in use.",
		}),
		dbWaitCount: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: operationsMetricNamespace,
			Name:      "db_wait_count",
			Help:      "Total waits for a database connection.",
		}),
		logFileBytes: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: operationsMetricNamespace,
			Name:      "log_file_bytes",
			Help:      "Current application file log size in bytes.",
		}),
		diskFreeBytes: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: operationsMetricNamespace,
			Name:      "disk_free_bytes",
			Help:      "Free bytes on the filesystem containing the application file log.",
		}),
		schemaVersion: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: operationsMetricNamespace,
			Name:      "schema_migration_version",
			Help:      "Schema migration version captured at application startup.",
		}),
		schemaDirty: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: operationsMetricNamespace,
			Name:      "schema_migration_dirty",
			Help:      "Whether the schema migration state captured at startup is dirty.",
		}),
		buildInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: operationsMetricNamespace,
			Name:      "build_info",
			Help:      "WeKnora build information.",
		}, []string{"version"}),
		backupScheduleEnabled: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: operationsMetricNamespace,
			Name:      "backup_schedule_enabled",
			Help:      "Whether scheduled MySQL backups are enabled.",
		}),
		backupLastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: operationsMetricNamespace,
			Name:      "backup_last_success_timestamp_seconds",
			Help:      "Unix timestamp of the last successful scheduled MySQL backup.",
		}),
		backupLastFailure: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: operationsMetricNamespace,
			Name:      "backup_last_failure_timestamp_seconds",
			Help:      "Unix timestamp of the last failed scheduled MySQL backup.",
		}),
		backupRetentionFailed: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: operationsMetricNamespace,
			Name:      "backup_retention_failure",
			Help:      "Whether the most recent scheduled backup retention sweep failed.",
		}),
	}

	registry.MustRegister(
		observer.httpRequests,
		observer.httpDuration,
		observer.httpInFlight,
		observer.dependencyUp,
		observer.dependencyConfigured,
		observer.dbOpenConnections,
		observer.dbInUseConnections,
		observer.dbWaitCount,
		observer.logFileBytes,
		observer.diskFreeBytes,
		observer.schemaVersion,
		observer.schemaDirty,
		observer.buildInfo,
		observer.backupScheduleEnabled,
		observer.backupLastSuccess,
		observer.backupLastFailure,
		observer.backupRetentionFailed,
	)
	observer.buildInfo.WithLabelValues(buildVersion()).Set(1)
	observer.metricsHandler = promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	return observer
}

func registerMetricsRoute(r gin.IRouter, observer *operationsObserver) {
	r.GET("/metrics", observer.metrics)
}

func RegisterOperationsAdminRoutes(r *gin.RouterGroup, observer *operationsObserver, g *rbacGuards) {
	operations := r.Group("/admin/operations", g.SystemAdmin())
	operations.GET("/status", observer.status)
	operations.POST("/backups", observer.createManualBackup)
}

func (o *operationsObserver) httpMetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path == "/metrics" {
			c.Next()
			return
		}

		started := time.Now()
		o.httpInFlight.Inc()
		defer func() {
			o.httpInFlight.Dec()
			status := c.Writer.Status()
			if status == 0 {
				status = http.StatusOK
			}
			statusClass := strconv.Itoa(status/100) + "xx"
			o.httpRequests.WithLabelValues(c.Request.Method, statusClass).Inc()
			o.httpDuration.WithLabelValues(c.Request.Method, statusClass).Observe(time.Since(started).Seconds())
		}()

		c.Next()
	}
}

func (o *operationsObserver) metrics(c *gin.Context) {
	o.collect(c.Request.Context())
	o.metricsHandler.ServeHTTP(c.Writer, c.Request)
}

func (o *operationsObserver) status(c *gin.Context) {
	c.JSON(http.StatusOK, o.collect(c.Request.Context()))
}

func (o *operationsObserver) collect(ctx context.Context) operationsStatusResponse {
	checks, failed := runReadinessChecks(ctx, defaultReadinessChecks(o.db, o.redisClient), readinessCheckTimeout)
	databaseStatus := o.collectDatabaseMetrics()
	fileLogStatus := o.collectFileLogMetrics()
	migrationStatus := o.collectMigrationMetrics()
	backupStatus := o.collectBackupMetrics()
	o.collectDependencyMetrics(checks)

	status := "ready"
	if failed {
		status = "not_ready"
	}
	return operationsStatusResponse{
		Status:        status,
		UptimeSeconds: int64(time.Since(o.startedAt).Seconds()),
		Dependencies:  checks,
		Database:      databaseStatus,
		FileLog:       fileLogStatus,
		Migration:     migrationStatus,
		Backup:        backupStatus,
	}
}

func (o *operationsObserver) collectDependencyMetrics(checks map[string]string) {
	databaseName := "database"
	if o.db != nil {
		switch o.db.Dialector.Name() {
		case "mysql", "postgres", "sqlite":
			databaseName = o.db.Dialector.Name()
		}
	}
	o.dependencyConfigured.WithLabelValues(databaseName).Set(1)
	o.dependencyUp.WithLabelValues(databaseName).Set(metricValue(checks["database"] == "ok"))

	redisConfigured := checks["redis"] != "disabled"
	o.dependencyConfigured.WithLabelValues("redis").Set(metricValue(redisConfigured))
	o.dependencyUp.WithLabelValues("redis").Set(metricValue(checks["redis"] == "ok"))
}

func (o *operationsObserver) collectDatabaseMetrics() operationsDatabaseStatus {
	if o.db == nil {
		o.dbOpenConnections.Set(0)
		o.dbInUseConnections.Set(0)
		o.dbWaitCount.Set(0)
		return operationsDatabaseStatus{Driver: "unknown"}
	}
	driver := o.db.Dialector.Name()
	if driver != "mysql" && driver != "postgres" && driver != "sqlite" {
		driver = "unknown"
	}

	sqlDB, err := o.db.DB()
	if err != nil {
		o.dbOpenConnections.Set(0)
		o.dbInUseConnections.Set(0)
		o.dbWaitCount.Set(0)
		return operationsDatabaseStatus{Driver: driver}
	}

	stats := sqlDB.Stats()
	o.dbOpenConnections.Set(float64(stats.OpenConnections))
	o.dbInUseConnections.Set(float64(stats.InUse))
	o.dbWaitCount.Set(float64(stats.WaitCount))
	return operationsDatabaseStatus{
		Driver:           driver,
		OpenConnections:  stats.OpenConnections,
		InUseConnections: stats.InUse,
		WaitCount:        int64(stats.WaitCount),
	}
}

func (o *operationsObserver) collectFileLogMetrics() operationsFileLogStatus {
	status, err := logger.GetFileLogRuntimeStatus()
	if err != nil {
		o.logFileBytes.Set(0)
		o.diskFreeBytes.Set(0)
		return operationsFileLogStatus{Enabled: status.Enabled, DiskState: status.DiskState}
	}

	o.logFileBytes.Set(float64(status.SizeBytes))
	o.diskFreeBytes.Set(float64(status.DiskFreeBytes))
	return operationsFileLogStatus{
		Enabled:        status.Enabled,
		SizeBytes:      status.SizeBytes,
		DiskFreeBytes:  status.DiskFreeBytes,
		DiskTotalBytes: status.DiskTotalBytes,
		DiskState:      status.DiskState,
	}
}

func (o *operationsObserver) startEmailAlerts(cleaner interfaces.ResourceCleaner) {
	alerts := newOperationsEmailAlerterFromEnv()
	if alerts == nil || cleaner == nil {
		return
	}

	stop := alerts.start(func(ctx context.Context) []operationsAlertCondition {
		return operationAlertConditions(o.collect(ctx))
	})
	cleaner.RegisterWithName("OperationsEmailAlerts", func() error {
		stop()
		return nil
	})
}

func (o *operationsObserver) startScheduledBackups(cleaner interfaces.ResourceCleaner) {
	if o.backupSchedule == nil {
		return
	}
	o.backupSchedule.SetRunHandler(o.emitScheduledBackupAudit)
	if err := o.backupSchedule.Start(); err != nil {
		logger.Warnf(context.Background(), "[backup] scheduled MySQL backup did not start; details suppressed")
	}
	if cleaner != nil {
		cleaner.RegisterWithName("MySQLBackupScheduler", func() error {
			o.backupSchedule.Stop()
			return nil
		})
	}
}

func (o *operationsObserver) collectMigrationMetrics() operationsMigrationStatus {
	version, dirty, known := database.CachedMigrationVersion()
	if !known {
		o.schemaVersion.Set(0)
		o.schemaDirty.Set(0)
		return operationsMigrationStatus{}
	}

	o.schemaVersion.Set(float64(version))
	o.schemaDirty.Set(metricValue(dirty))
	return operationsMigrationStatus{Known: true, Version: version, Dirty: dirty}
}

func (o *operationsObserver) collectBackupMetrics() operationsBackupStatus {
	if o.backupSchedule == nil {
		o.backupScheduleEnabled.Set(0)
		o.backupLastSuccess.Set(0)
		o.backupLastFailure.Set(0)
		o.backupRetentionFailed.Set(0)
		return operationsBackupStatus{}
	}
	status := o.backupSchedule.Status()
	o.backupScheduleEnabled.Set(metricValue(status.Enabled))
	o.backupLastSuccess.Set(timeOrZero(status.LastSuccessAt))
	o.backupLastFailure.Set(timeOrZero(status.LastFailureAt))
	o.backupRetentionFailed.Set(metricValue(!status.LastRetentionFailureAt.IsZero()))
	return operationsBackupStatus{
		Scheduled:              status.Enabled,
		ConfigurationError:     status.ConfigurationError,
		Schedule:               status.Schedule,
		RetentionDays:          status.RetentionDays,
		MinFreeGB:              status.MinFreeGB,
		LastSuccessAt:          status.LastSuccessAt,
		LastFailureAt:          status.LastFailureAt,
		LastFailureKind:        status.LastFailureKind,
		LastRetentionAt:        status.LastRetentionAt,
		LastRetentionFailureAt: status.LastRetentionFailureAt,
		LastRetentionDeleted:   status.LastRetentionDeleted,
	}
}

func timeOrZero(value time.Time) float64 {
	if value.IsZero() {
		return 0
	}
	return float64(value.Unix())
}

func metricValue(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func buildVersion() string {
	if version := strings.TrimSpace(os.Getenv("WEKNORA_VERSION")); version != "" {
		return version
	}
	return "dev"
}
