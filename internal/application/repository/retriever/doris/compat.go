package doris

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
)

const envDorisCompatMode = "DORIS_COMPAT_MODE"

type dorisCompatMode string

const (
	dorisCompatModeAuto                   dorisCompatMode = "auto"
	dorisCompatModeLegacy                 dorisCompatMode = "legacy"
	dorisCompatModeInnerProductDuplicate  dorisCompatMode = "inner_product_duplicate"
)

type dorisCompatProbe struct {
	innerProductApproximate  bool
	cosineDistanceApproximate bool
}

func resolveConfiguredDorisCompatMode() (mode dorisCompatMode, invalid string) {
	raw := strings.TrimSpace(os.Getenv(envDorisCompatMode))
	if raw == "" {
		return dorisCompatModeAuto, ""
	}

	switch strings.ToLower(raw) {
	case string(dorisCompatModeAuto):
		return dorisCompatModeAuto, ""
	case string(dorisCompatModeLegacy):
		return dorisCompatModeLegacy, ""
	case string(dorisCompatModeInnerProductDuplicate), "inner-product-duplicate", "inner_product", "inner-product":
		return dorisCompatModeInnerProductDuplicate, ""
	default:
		return dorisCompatModeAuto, raw
	}
}

func (r *dorisRepository) resolveCompatMode(ctx context.Context) (dorisCompatMode, error) {
	r.compatResolveOnce.Do(func() {
		log := logger.GetLogger(ctx)
		requested := r.compatModeRequested
		if requested == "" {
			r.compatModeResolved = dorisCompatModeInnerProductDuplicate
			return
		}

		existingMode, exampleTable, foundExisting, err := r.detectExistingCompatMode(ctx)
		if err != nil {
			r.compatResolveErr = err
			return
		}

		if foundExisting {
			if requested != dorisCompatModeAuto && requested != existingMode {
				r.compatResolveErr = fmt.Errorf(
					"Doris compat mode %q does not match existing embedding tables (detected %q from %s). %s is not interchangeable after %s_* tables are created. Recreate the existing %s_* tables before switching modes, or set %s=%s",
					requested,
					existingMode,
					exampleTable,
					envDorisCompatMode,
					r.tableBaseName,
					r.tableBaseName,
					envDorisCompatMode,
					existingMode,
				)
				log.Errorf("[Doris] %v", r.compatResolveErr)
				return
			}

			r.compatModeResolved = existingMode
			log.Warnf(
				"[Doris] Using compat mode %s from existing embedding tables (%s). %s is not interchangeable after %s_* tables are created; recreate those tables before switching modes.",
				existingMode,
				exampleTable,
				envDorisCompatMode,
				r.tableBaseName,
			)
			return
		}

		if requested == dorisCompatModeAuto {
			probe, probeErr := r.probeCompatMode(ctx)
			if probeErr != nil {
				r.compatResolveErr = probeErr
				log.Errorf("[Doris] %v", probeErr)
				return
			}
			log.Infof(
				"[Doris] Compat probe result: inner_product_approximate=%t, cosine_distance_approximate=%t",
				probe.innerProductApproximate,
				probe.cosineDistanceApproximate,
			)

			switch {
			case probe.innerProductApproximate:
				r.compatModeResolved = dorisCompatModeInnerProductDuplicate
			case probe.cosineDistanceApproximate:
				r.compatModeResolved = dorisCompatModeLegacy
			default:
				r.compatResolveErr = fmt.Errorf(
					"Doris compatibility auto-detection could not find a supported vector function. Set %s=%s or %s=%s explicitly after verifying your Doris build. %s is not interchangeable after %s_* tables are created",
					envDorisCompatMode,
					dorisCompatModeInnerProductDuplicate,
					envDorisCompatMode,
					dorisCompatModeLegacy,
					envDorisCompatMode,
					r.tableBaseName,
				)
				log.Errorf("[Doris] %v", r.compatResolveErr)
				return
			}

			log.Warnf(
				"[Doris] Auto-selected compat mode %s for new embedding tables. %s is not interchangeable after %s_* tables are created; recreate those tables before switching modes.",
				r.compatModeResolved,
				envDorisCompatMode,
				r.tableBaseName,
			)
			return
		}

		r.compatModeResolved = requested
		log.Warnf(
			"[Doris] Using configured compat mode %s for new embedding tables. %s is not interchangeable after %s_* tables are created; recreate those tables before switching modes.",
			r.compatModeResolved,
			envDorisCompatMode,
			r.tableBaseName,
		)
	})

	return r.compatModeResolved, r.compatResolveErr
}

func (r *dorisRepository) detectExistingCompatMode(ctx context.Context) (dorisCompatMode, string, bool, error) {
	tables, err := r.listEmbeddingTables(ctx)
	if err != nil {
		return "", "", false, fmt.Errorf("list Doris embedding tables: %w", err)
	}
	if len(tables) == 0 {
		return "", "", false, nil
	}
	sort.Strings(tables)

	var detected dorisCompatMode
	for _, table := range tables {
		ddl, err := r.showCreateTable(ctx, table)
		if err != nil {
			return "", "", false, fmt.Errorf("show create table %s: %w", table, err)
		}
		mode, err := compatModeFromDDL(ddl)
		if err != nil {
			return "", "", false, fmt.Errorf("detect compat mode from %s: %w", table, err)
		}
		if detected == "" {
			detected = mode
			continue
		}
		if detected != mode {
			return "", "", false, fmt.Errorf(
				"existing Doris embedding tables use mixed compat modes (%s and %s). %s is not interchangeable after %s_* tables are created; recreate the existing %s_* tables with a single mode",
				detected,
				mode,
				envDorisCompatMode,
				r.tableBaseName,
				r.tableBaseName,
			)
		}
	}

	return detected, tables[0], true, nil
}

func (r *dorisRepository) showCreateTable(ctx context.Context, table string) (string, error) {
	row := r.db.QueryRowContext(ctx, fmt.Sprintf("SHOW CREATE TABLE `%s`", table))
	var tableName, ddl string
	if err := row.Scan(&tableName, &ddl); err != nil {
		return "", err
	}
	return ddl, nil
}

func compatModeFromDDL(ddl string) (dorisCompatMode, error) {
	normalized := strings.ToLower(ddl)
	switch {
	case strings.Contains(normalized, "duplicate key("):
		return dorisCompatModeInnerProductDuplicate, nil
	case strings.Contains(normalized, "unique key("):
		return dorisCompatModeLegacy, nil
	default:
		return "", fmt.Errorf("unsupported table definition; expected UNIQUE KEY or DUPLICATE KEY")
	}
}

func (r *dorisRepository) probeCompatMode(ctx context.Context) (dorisCompatProbe, error) {
	probe := dorisCompatProbe{}
	probe.innerProductApproximate = r.vectorFunctionSupported(ctx, "inner_product_approximate([1.0],[1.0])")
	probe.cosineDistanceApproximate = r.vectorFunctionSupported(ctx, "cosine_distance_approximate([1.0],[1.0])")
	return probe, nil
}

func (r *dorisRepository) vectorFunctionSupported(ctx context.Context, expr string) bool {
	var value sql.NullFloat64
	if err := r.db.QueryRowContext(ctx, "SELECT "+expr).Scan(&value); err != nil {
		return false
	}
	return true
}

func (m dorisCompatMode) normalizeEmbeddings() bool {
	return m != dorisCompatModeLegacy
}

func (m dorisCompatMode) usesReplaceWrite() bool {
	return m != dorisCompatModeLegacy
}

func (m dorisCompatMode) usesRewriteChunkUpdates() bool {
	return m != dorisCompatModeLegacy
}
