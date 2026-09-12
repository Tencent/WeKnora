package database

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed migration_profiles.json.gz
var migrationProfilesGzip []byte

type migrationFile struct {
	Path, SQL, SHA256 string
	Version           int
}
type migrationFiles struct {
	Files  map[string][]migrationFile
	Hashes map[string]string
	SHA256 string
}
type (
	schemaObjects     map[string]string
	migrationProfiles struct {
		Format        int
		Files         map[string]string
		Official      map[string]schemaObjects
		Topic3        map[string]schemaObjects
		SQLiteRuntime map[string]schemaObjects
	}
)

func (f *migrationFiles) chain(dialect, chain string) []migrationFile {
	return f.Files[dialect+"/"+chain]
}

func hashJSON(value any) string {
	data, _ := json.Marshal(value)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func profileKey(dialect, variant string, version int) string {
	return fmt.Sprintf("%s/%s/%d", dialect, variant, version)
}

func readMigrationFiles(root string) (*migrationFiles, error) {
	if root == "" {
		root = "migrations"
	}
	files := &migrationFiles{Files: map[string][]migrationFile{}, Hashes: map[string]string{}}
	for _, input := range [][3]string{
		{"postgres", "official", "versioned"},
		{"sqlite", "official", "sqlite"},
		{"postgres", "topic3", "topic3/postgres"},
		{"sqlite", "topic3", "topic3/sqlite"},
	} {
		paths, err := filepath.Glob(filepath.Join(root, input[2], "*.up.sql"))
		if err != nil {
			return nil, err
		}
		if len(paths) == 0 {
			return nil, fmt.Errorf("missing migration chain %s", input[2])
		}
		seen := map[int]bool{}
		for _, path := range paths {
			version, err := strconv.Atoi(strings.SplitN(filepath.Base(path), "_", 2)[0])
			if err != nil || seen[version] {
				return nil, fmt.Errorf("invalid or duplicate migration version: %s", path)
			}
			seen[version] = true
			for _, paired := range []string{path, strings.TrimSuffix(path, ".up.sql") + ".down.sql"} {
				data, err := os.ReadFile(paired)
				if err != nil {
					return nil, fmt.Errorf("missing migration pair: %s", paired)
				}
				relative, _ := filepath.Rel(root, paired)
				normalized := strings.ReplaceAll(string(data), "\r\n", "\n")
				hash := sha256.Sum256([]byte(normalized))
				files.Hashes[filepath.ToSlash(relative)] = hex.EncodeToString(hash[:])
			}
			data, _ := os.ReadFile(path)
			relative, _ := filepath.Rel(root, path)
			relative = filepath.ToSlash(relative)
			files.Files[input[0]+"/"+input[1]] = append(
				files.Files[input[0]+"/"+input[1]],
				migrationFile{Path: relative, SQL: string(data), Version: version, SHA256: files.Hashes[relative]},
			)
		}
		chain := files.Files[input[0]+"/"+input[1]]
		sort.Slice(chain, func(i, j int) bool { return chain[i].Version < chain[j].Version })
		start := 0
		if input[1] == "topic3" {
			start = 1
		}
		for i, file := range chain {
			if file.Version != start+i {
				return nil, fmt.Errorf("non-contiguous chain at %s", file.Path)
			}
		}
	}
	files.SHA256 = hashJSON(files.Hashes)
	return files, nil
}

var (
	profileOnce        sync.Once
	cachedProfiles     *migrationProfiles
	cachedProfileError error
)

func readProfiles() (*migrationProfiles, error) {
	profileOnce.Do(func() { cachedProfiles, cachedProfileError = decodeProfiles() })
	return cachedProfiles, cachedProfileError
}

func decodeProfiles() (*migrationProfiles, error) {
	gz, err := gzip.NewReader(bytes.NewReader(migrationProfilesGzip))
	if err != nil {
		return nil, fmt.Errorf("migration profiles unavailable: %w", err)
	}
	defer func() { _ = gz.Close() }()
	var profiles migrationProfiles
	if err := json.NewDecoder(gz).Decode(&profiles); err != nil {
		return nil, err
	}
	if profiles.Format != 1 {
		return nil, fmt.Errorf("unsupported migration profile format")
	}
	return &profiles, nil
}

func loadMigrationFiles(root string) (*migrationFiles, error) {
	files, err := readMigrationFiles(root)
	if err != nil {
		return nil, err
	}
	profiles, err := readProfiles()
	if err != nil {
		return nil, err
	}
	if hashJSON(profiles.Files) != files.SHA256 {
		return nil, fmt.Errorf("migration files differ from the verified structure profile manifest")
	}
	return files, nil
}
func quoted(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
func metadataTable(table string) bool {
	return table == OfficialMigrationTable || table == Topic3MigrationTable || table == MigrationBridgeTable
}

func topicTable(dialect, table string) bool {
	if dialect == "sqlite" &&
		(table == "tenant_skills" || table == "tenant_skill_snapshots" || table == "tenant_user_env_vars" ||
			table == "tenant_skill_catalog") {
		return true
	}
	return strings.HasPrefix(table, "evaluation_") || strings.HasPrefix(table, "embedding_cache_") ||
		table == "model_price_versions" ||
		table == "model_call_records"
}
func tableOfObject(key string) string { return strings.SplitN(key, "/", 2)[0] }

func describeSchema(ctx context.Context, q sqlQueryer, dialect string) (schemaObjects, map[string]bool, error) {
	objects, tables := schemaObjects{}, map[string]bool{}
	if dialect == "sqlite" {
		rows, err := q.QueryContext(ctx, "SELECT type,name,tbl_name,coalesce(sql,'') FROM sqlite_master WHERE "+
			"name NOT LIKE 'sqlite_%' ORDER BY type,name")
		if err != nil {
			return nil, nil, err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var kind, name, table, definition string
			if err := rows.Scan(&kind, &name, &table, &definition); err != nil {
				return nil, nil, err
			}
			if kind == "table" {
				tables[table] = true
			}
			if !metadataTable(table) {
				objects[table+"/"+kind+"/"+name] = strings.ReplaceAll(strings.TrimSpace(definition), "\r\n", "\n")
			}
		}
		return objects, tables, rows.Err()
	}
	var schema string
	if err := q.QueryRowContext(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		return nil, nil, err
	}
	normalize := func(s string) string {
		s = strings.ReplaceAll(s, quoted(schema)+".", "")
		return strings.ReplaceAll(s, schema+".", "")
	}
	rows, err := q.QueryContext(ctx, "SELECT c.relname,a.attname,format_type(a.atttypid,a.atttypmod),"+
		"a.attnotnull,coalesce(pg_get_expr(d.adbin,d.adrelid),'')\n\tFROM "+
		"pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN "+
		"pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT "+
		"a.attisdropped\n\tLEFT JOIN pg_attrdef d ON d.adrelid=c.oid AND "+
		"d.adnum=a.attnum WHERE n.nspname=current_schema() AND c.relkind IN "+
		"('r','p') AND NOT EXISTS(SELECT 1 FROM pg_depend dep JOIN "+
		"pg_extension ext ON ext.oid=dep.refobjid WHERE "+
		"dep.classid='pg_class'::regclass AND dep.objid=c.oid AND "+
		"dep.refclassid='pg_extension'::regclass AND dep.deptype='e') ORDER BY "+
		"c.relname,a.attnum")
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var table, column, typ, def string
		var notNull bool
		if err := rows.Scan(&table, &column, &typ, &notNull, &def); err != nil {
			_ = rows.Close()
			return nil, nil, err
		}
		tables[table] = true
		if !metadataTable(table) {
			objects[table+"/column/"+column] = fmt.Sprintf("%s|%t|%s", typ, notNull, normalize(def))
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, nil, err
	}
	_ = rows.Close()
	queries := []struct{ kind, query string }{
		{"constraint", "SELECT t.relname,c.conname,c.contype::text || '|' || " +
			"c.convalidated::text || '|' || pg_get_constraintdef(c.oid,true) FROM " +
			"pg_constraint c JOIN pg_class t ON t.oid=c.conrelid JOIN pg_namespace " +
			"n ON n.oid=t.relnamespace WHERE n.nspname=current_schema() AND NOT " +
			"EXISTS(SELECT 1 FROM pg_depend dep JOIN pg_extension ext ON " +
			"ext.oid=dep.refobjid WHERE dep.classid='pg_class'::regclass AND " +
			"dep.objid=t.oid AND dep.refclassid='pg_extension'::regclass AND " +
			"dep.deptype='e')"},
		{"index", "SELECT t.relname,c.relname,pg_get_indexdef(c.oid) || '|' || " +
			"i.indisvalid::text FROM pg_index i JOIN pg_class c ON " +
			"c.oid=i.indexrelid JOIN pg_class t ON t.oid=i.indrelid JOIN " +
			"pg_namespace n ON n.oid=t.relnamespace WHERE " +
			"n.nspname=current_schema() AND NOT EXISTS(SELECT 1 FROM pg_depend dep " +
			"JOIN pg_extension ext ON ext.oid=dep.refobjid WHERE " +
			"dep.classid='pg_class'::regclass AND dep.objid=t.oid AND " +
			"dep.refclassid='pg_extension'::regclass AND dep.deptype='e')"},
		{"trigger", "SELECT c.relname,t.tgname,pg_get_triggerdef(t.oid,true) FROM " +
			"pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n " +
			"ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND NOT " +
			"t.tgisinternal AND NOT EXISTS(SELECT 1 FROM pg_depend dep JOIN " +
			"pg_extension ext ON ext.oid=dep.refobjid WHERE " +
			"dep.classid='pg_class'::regclass AND dep.objid=c.oid AND " +
			"dep.refclassid='pg_extension'::regclass AND dep.deptype='e')"},
	}
	for _, item := range queries {
		rows, err := q.QueryContext(ctx, item.query)
		if err != nil {
			return nil, nil, err
		}
		for rows.Next() {
			var table, name, definition string
			if err := rows.Scan(&table, &name, &definition); err != nil {
				_ = rows.Close()
				return nil, nil, err
			}
			if !metadataTable(table) {
				objects[table+"/"+item.kind+"/"+name] = normalize(definition)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, nil, err
		}
		_ = rows.Close()
	}
	return objects, tables, nil
}

func readChainVersion(ctx context.Context, q sqlQueryer, table string, exists bool) (int, bool, error) {
	if !exists {
		return -1, false, nil
	}
	rows, err := q.QueryContext(ctx, "SELECT version,dirty FROM "+table)
	if err != nil {
		return -1, false, fmt.Errorf("invalid %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	version, dirty, count := -1, false, 0
	for rows.Next() {
		count++
		if err := rows.Scan(&version, &dirty); err != nil {
			return -1, false, err
		}
	}
	if err := rows.Err(); err != nil {
		return -1, false, err
	}
	if count > 1 || version < -1 || (count == 1 && version < 0) {
		return -1, false, fmt.Errorf("invalid version rows in %s", table)
	}
	if dirty {
		return version, dirty, fmt.Errorf(
			"dirty migration chain %s at version %d; verified repair required",
			table,
			version,
		)
	}
	return version, dirty, nil
}

func expectedSchema(p *migrationProfiles, dialect, variant string, official, topic int) (schemaObjects, bool) {
	out := schemaObjects{}
	if official >= 0 {
		base, ok := p.Official[profileKey(dialect, variant, official)]
		if !ok {
			return nil, false
		}
		for k, v := range base {
			out[k] = v
		}
	}
	if topic >= 1 {
		extra, ok := p.Topic3[profileKey(dialect, "topic3", topic)]
		if !ok {
			return nil, false
		}
		for k, v := range extra {
			out[k] = v
		}
	}
	return out, true
}

func schemaDifference(actual, expected schemaObjects) string {
	keys := []string{}
	for key, value := range expected {
		if got, ok := actual[key]; !ok || normalizeSchemaDefinition(got) != normalizeSchemaDefinition(value) {
			keys = append(keys, key)
		}
	}
	for key := range actual {
		if _, ok := expected[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) > 12 {
		keys = keys[:12]
	}
	return strings.Join(keys, ", ")
}

func validateAppendOnlyManifest(prior, current map[string]string) error {
	if len(prior) == 0 {
		return fmt.Errorf("bridge input manifest is empty")
	}
	maximum := map[string]int{}
	for path, hash := range prior {
		if current[path] != hash {
			return fmt.Errorf("previous migration content is missing or changed: %s", path)
		}
		dir := filepath.ToSlash(filepath.Dir(path))
		version, err := strconv.Atoi(strings.SplitN(filepath.Base(path), "_", 2)[0])
		if err != nil {
			return fmt.Errorf("invalid prior migration path")
		}
		if version > maximum[dir] {
			maximum[dir] = version
		}
	}
	for path := range current {
		if _, exists := prior[path]; exists {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(path))
		version, err := strconv.Atoi(strings.SplitN(filepath.Base(path), "_", 2)[0])
		if err != nil || version <= maximum[dir] {
			return fmt.Errorf("migration additions must append to a chain: %s", path)
		}
	}
	return nil
}

func inspectMigrationState(
	ctx context.Context,
	q sqlQueryer,
	dialect string,
	files *migrationFiles,
) (
	*MigrationStatus,
	error,
) {
	profiles, err := readProfiles()
	if err != nil {
		return nil, err
	}
	objects, tables, err := describeSchema(ctx, q, dialect)
	if err != nil {
		return nil, err
	}
	official, dirty, err := readChainVersion(ctx, q, OfficialMigrationTable, tables[OfficialMigrationTable])
	if err != nil {
		return nil, err
	}
	if dialect == "sqlite" {
		if official < 0 && len(objects) > 0 {
			return nil, fmt.Errorf("unversioned nonempty database is not an accepted source")
		}
		if err := validateSQLiteRuntimeObjects(objects, tables, profiles.SQLiteRuntime); err != nil {
			return nil, err
		}
	}
	s := &MigrationStatus{
		Dialect:         dialect,
		InputSHA256:     files.SHA256,
		InputManifest:   files.Hashes,
		SourceVersion:   official,
		StructureSHA256: hashSchema(objects),
		Official:        MigrationChainState{Version: official, Dirty: dirty},
		Topic3:          MigrationChainState{Version: -1},
		Phase:           "unaccepted",
		Pending:         []string{},
	}
	for _, chain := range []string{"official", "topic3"} {
		list := files.chain(dialect, chain)
		if chain == "official" {
			s.Official.ExpectedVersion = list[len(list)-1].Version
		} else {
			s.Topic3.ExpectedVersion = list[len(list)-1].Version
		}
	}
	variant := "plain"
	if dialect == "postgres" && tables["embeddings"] {
		variant = "vector"
	}
	if tables[MigrationBridgeTable] {
		if !tables[OfficialMigrationTable] || !tables[Topic3MigrationTable] {
			return s, fmt.Errorf("bridge exists without both version tables")
		}
		topic, dirty, err := readChainVersion(ctx, q, Topic3MigrationTable, true)
		if err != nil {
			return s, err
		}
		s.Topic3.Version, s.Topic3.Dirty = topic, dirty
		rows, err := q.QueryContext(ctx, "SELECT id,source_profile,phase,official_version,topic3_version,"+
			"input_sha256,input_manifest,source_version,source_official,"+
			"source_topic3,source_structure_sha256,source_database_sha256,"+
			"source_dirty,profile_version,official_target,topic3_target,backup_id,"+
			"started_at,updated_at,completed_at,error_code FROM "+
			"migration_bridge_runs")
		if err != nil {
			return s, fmt.Errorf("invalid bridge audit: %w", err)
		}
		count := 0
		var receiptOfficial, receiptTopic int
		var input, manifest string
		var sourceOfficial, sourceTopic, profileVersion, officialTarget, topicTarget int
		var sourceStructure, sourceDatabase, backup, started, updated, errorCode string
		var sourceDirty bool
		var completed sql.NullString
		for rows.Next() {
			count++
			if err := rows.Scan(
				&s.RunID,
				&s.Source,
				&s.Phase,
				&receiptOfficial,
				&receiptTopic,
				&input,
				&manifest,
				&s.SourceVersion,
				&sourceOfficial,
				&sourceTopic,
				&sourceStructure,
				&sourceDatabase,
				&sourceDirty,
				&profileVersion,
				&officialTarget,
				&topicTarget,
				&backup,
				&started,
				&updated,
				&completed,
				&errorCode,
			); err != nil {
				_ = rows.Close()
				return s, err
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return s, err
		}
		_ = rows.Close()
		var prior map[string]string
		if err := json.Unmarshal([]byte(manifest), &prior); err != nil {
			return s, fmt.Errorf("invalid bridge input manifest")
		}
		if count != 1 || receiptOfficial != official || receiptTopic != topic || hashJSON(prior) != input ||
			len(s.RunID) != 32 {
			return s, fmt.Errorf("bridge receipt disagrees with migration inputs or versions")
		}
		if err := validateAppendOnlyManifest(prior, files.Hashes); err != nil {
			return s, err
		}
		if s.Phase != "accepted" && s.Phase != "official" && s.Phase != "topic3" && s.Phase != "complete" {
			return s, fmt.Errorf("unknown bridge phase")
		}
		if profileVersion != 1 || sourceDirty || len(sourceDatabase) != 64 ||
			officialTarget > s.Official.ExpectedVersion ||
			topicTarget > s.Topic3.ExpectedVersion ||
			receiptOfficial > officialTarget ||
			receiptTopic > topicTarget {
			return s, fmt.Errorf("invalid bridge provenance or targets")
		}
		if _, err := time.Parse(time.RFC3339Nano, started); err != nil {
			return s, fmt.Errorf("invalid bridge start time")
		}
		if _, err := time.Parse(time.RFC3339Nano, updated); err != nil {
			return s, fmt.Errorf("invalid bridge update time")
		}
		if s.Phase == "complete" {
			if !completed.Valid || errorCode != "" {
				return s, fmt.Errorf("incomplete bridge completion evidence")
			}
			if _, err := time.Parse(time.RFC3339Nano, completed.String); err != nil {
				return s, fmt.Errorf("invalid bridge completion time")
			}
		}
		if err := validateSourceReceipt(profiles, s, sourceOfficial, sourceTopic, sourceStructure, backup); err != nil {
			return s, err
		}
		expected, ok := expectedSchema(profiles, dialect, variant, official, topic)
		if !ok {
			return s, fmt.Errorf("unknown dual-chain version")
		}
		if diff := schemaDifference(objects, expected); diff != "" {
			return s, fmt.Errorf("dual-chain structure mismatch: %s", diff)
		}
		if err := validateVersionIndexes(ctx, q, dialect); err != nil {
			return s, err
		}
	} else {
		if tables[Topic3MigrationTable] {
			return s, fmt.Errorf("topic3 version table exists without bridge receipt")
		}
		baseline, offset := 89, 89
		if dialect == "sqlite" {
			baseline, offset = 12, 12
		}
		if official < 0 {
			if len(objects) != 0 {
				return s, fmt.Errorf("unversioned nonempty database is not an accepted source")
			}
			s.Source = "empty"
		} else {
			matched := false
			var diff string
			if expected, ok := expectedSchema(profiles, dialect, variant, official, -1); ok {
				diff = schemaDifference(objects, expected)
				if diff == "" {
					s.Source = fmt.Sprintf("official-%s-%d", dialect, official)
					matched = true
				}
			}
			if !matched && official > offset && official <= offset+14 {
				topic := official - offset
				if expected, ok := expectedSchema(profiles, dialect, variant, baseline, topic); ok {
					diff = schemaDifference(objects, expected)
					if diff == "" {
						s.Source = fmt.Sprintf("topic3-%s-%d", dialect, official)
						s.Official.Version = baseline
						s.Topic3.Version = topic
						matched = true
					}
				}
			}
			if !matched {
				return s, fmt.Errorf("unknown or partial source structure at version %d: %s", official, diff)
			}
		}
	}
	if err := validateMigrationData(ctx, q, dialect, tables); err != nil {
		return s, err
	}
	for _, chain := range []string{"official", "topic3"} {
		version := s.Official.Version
		if chain == "topic3" {
			version = s.Topic3.Version
		}
		for _, file := range files.chain(dialect, chain) {
			if file.Version > version {
				s.Pending = append(s.Pending, file.Path)
			}
		}
	}
	s.Ready = s.Phase == "complete" && len(s.Pending) == 0
	return s, nil
}

func validateVersionIndexes(ctx context.Context, q sqlQueryer, dialect string) error {
	for _, table := range []string{OfficialMigrationTable, Topic3MigrationTable} {
		var count int
		if dialect == "sqlite" {
			err := q.QueryRowContext(ctx, "SELECT count(*) FROM pragma_index_list(?) WHERE name=? AND [unique]=1 "+
				"AND partial=0 AND (SELECT count(*) FROM pragma_index_info(?))=1 AND "+
				"(SELECT name FROM pragma_index_info(?) LIMIT 1)='version'",
				table, table+"_version_unique", table+"_version_unique", table+"_version_unique",
			).Scan(&count)
			if err != nil {
				return err
			}
		} else {
			err := q.QueryRowContext(ctx, "SELECT count(*) FROM pg_index i JOIN pg_class idx ON "+
				"idx.oid=i.indexrelid JOIN pg_class tbl ON tbl.oid=i.indrelid JOIN "+
				"pg_namespace n ON n.oid=tbl.relnamespace JOIN pg_attribute a ON "+
				"a.attrelid=tbl.oid AND a.attnum=i.indkey[0] WHERE "+
				"n.nspname=current_schema() AND tbl.relname=$1 AND idx.relname=$2 AND "+
				"i.indisunique AND i.indisvalid AND i.indisready AND i.indnatts=1 AND "+
				"i.indpred IS NULL AND i.indexprs IS NULL AND a.attname='version'",
				table, table+"_version_unique",
			).Scan(&count)
			if err != nil {
				return err
			}
		}
		if count != 1 {
			return fmt.Errorf("missing dedicated version index for %s", table)
		}
	}
	return nil
}

func validateSourceReceipt(
	p *migrationProfiles,
	s *MigrationStatus,
	official, topic int,
	digest, backup string,
) error {
	expectedSource := fmt.Sprintf("official-%s-%d", s.Dialect, s.SourceVersion)
	if s.SourceVersion < 0 {
		expectedSource = "empty"
		if official != -1 || topic != -1 {
			return fmt.Errorf("invalid empty source mapping")
		}
	} else if topic >= 1 {
		expectedSource = fmt.Sprintf("topic3-%s-%d", s.Dialect, s.SourceVersion)
		offset := 89
		if s.Dialect == "sqlite" {
			offset = 12
		}
		if official != offset || topic > 14 || s.SourceVersion != offset+topic {
			return fmt.Errorf("invalid topic3 source mapping")
		}
	} else if official != s.SourceVersion || topic != -1 {
		return fmt.Errorf("invalid official source mapping")
	}
	if s.Source != expectedSource || (s.Source != "empty" && strings.TrimSpace(backup) == "") {
		return fmt.Errorf("invalid source provenance or backup identity")
	}
	for _, variant := range []string{"plain", "vector"} {
		if objects, ok := expectedSchema(p, s.Dialect, variant, official, topic); ok &&
			(hashSchema(objects) == digest || hashJSON(objects) == digest) {
			return nil
		}
	}
	return fmt.Errorf("source structure receipt is inconsistent")
}

func validateMigrationData(ctx context.Context, q sqlQueryer, dialect string, tables map[string]bool) error {
	if dialect == "sqlite" {
		rows, err := q.QueryContext(ctx, "PRAGMA foreign_key_check")
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		if rows.Next() {
			return fmt.Errorf("database has orphan foreign-key rows")
		}
		if err := rows.Err(); err != nil {
			return err
		}
		_ = rows.Close()
		return validateSkillCatalog(ctx, q, tables)
	}
	rows, err := q.QueryContext(ctx, "SELECT src.relname,dst.relname,coalesce(ns.nspname,'public'),"+
		"\n\t(SELECT json_agg(a.attname ORDER BY k.ord)::text FROM "+
		"unnest(c.conkey) WITH ORDINALITY k(n,ord) JOIN pg_attribute a ON "+
		"a.attrelid=c.conrelid AND a.attnum=k.n),\n\t(SELECT json_agg(a.attname "+
		"ORDER BY k.ord)::text FROM unnest(c.confkey) WITH ORDINALITY k(n,ord) "+
		"JOIN pg_attribute a ON a.attrelid=c.confrelid AND a.attnum=k.n)\n\tFROM "+
		"pg_constraint c JOIN pg_class src ON src.oid=c.conrelid JOIN pg_class "+
		"dst ON dst.oid=c.confrelid JOIN pg_namespace n ON "+
		"n.oid=src.relnamespace JOIN pg_namespace ns ON "+
		"ns.oid=dst.relnamespace WHERE c.contype='f' AND "+
		"n.nspname=current_schema()")
	if err != nil {
		return err
	}
	type fk struct {
		src, dst, schema string
		left, right      []string
	}
	keys := []fk{}
	for rows.Next() {
		var f fk
		var left, right string
		if err := rows.Scan(&f.src, &f.dst, &f.schema, &left, &right); err != nil {
			_ = rows.Close()
			return err
		}
		if err := json.Unmarshal([]byte(left), &f.left); err != nil {
			_ = rows.Close()
			return err
		}
		if err := json.Unmarshal([]byte(right), &f.right); err != nil {
			_ = rows.Close()
			return err
		}
		keys = append(keys, f)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	for _, f := range keys {
		matches, nonNull := []string{}, []string{}
		for i, col := range f.left {
			matches = append(matches, "s."+quoted(col)+"=d."+quoted(f.right[i]))
			nonNull = append(nonNull, "s."+quoted(col)+" IS NOT NULL")
		}
		var orphan bool
		query := "SELECT EXISTS(SELECT 1 FROM " + quoted(f.src) + " s WHERE " + strings.Join(nonNull, " AND ") +
			" AND NOT EXISTS(SELECT 1 FROM " +
			quoted(f.schema) +
			"." +
			quoted(f.dst) +
			" d WHERE " +
			strings.Join(matches, " AND ") +
			"))"
		if err := q.QueryRowContext(ctx, query).Scan(&orphan); err != nil {
			return err
		}
		if orphan {
			return fmt.Errorf("orphan foreign-key rows in %s", f.src)
		}
	}
	return validateSkillCatalog(ctx, q, tables)
}

func validateSkillCatalog(ctx context.Context, q sqlQueryer, tables map[string]bool) error {
	if tables["tenant_skill_catalog"] {
		var invalid bool
		err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM tenant_skills s LEFT JOIN "+
			"tenant_skill_catalog c ON c.id=s.catalog_id AND c.deleted_at IS NULL "+
			"AND c.tenant_id=s.tenant_id AND c.name=s.name WHERE s.deleted_at IS "+
			"NULL AND c.id IS NULL)").Scan(&invalid)
		if err != nil {
			return err
		}
		if invalid {
			return fmt.Errorf("active skill installation has no matching tenant catalog entry")
		}
	}
	return nil
}

// WriteMigrationProfiles generates reference schemas only in a caller-owned
// isolated PostgreSQL database whose name starts with weknora_x03_.
func WriteMigrationProfiles(
	ctx context.Context,
	dsn, root, destination string,
	runtimeSource ...func(
		string,
		int,
	) error,
) error {
	files, err := readMigrationFiles(root)
	if err != nil {
		return err
	}
	p := &migrationProfiles{
		Format:   1,
		Files:    files.Hashes,
		Official: map[string]schemaObjects{},
		Topic3:   map[string]schemaObjects{},
	}
	for _, dialect := range []string{"sqlite", "postgres"} {
		variants := []string{"plain"}
		if dialect == "postgres" {
			variants = append(variants, "vector")
		}
		for _, variant := range variants {
			opts := MigrationOptions{}
			var connectionDSN string
			if dialect == "sqlite" {
				temp, err := os.CreateTemp("", "weknora-profile-*.sqlite")
				if err != nil {
					return err
				}
				_ = temp.Close()
				defer func() { _ = os.Remove(temp.Name()) }()
				opts.SQLiteDBPath = temp.Name()
				connectionDSN = "sqlite3://unused"
			} else {
				connectionDSN, err = createIsolatedMigrationDatabase(ctx, dsn, "profile_"+variant)
				if err != nil {
					return err
				}
			}
			m, err := openMigrationConnection(ctx, connectionDSN, opts, false)
			if err != nil {
				return err
			}
			if dialect == "postgres" {
				var name string
				if err := m.conn.QueryRowContext(ctx, "SELECT current_database()").Scan(&name); err != nil {
					m.close()
					return err
				}
				if !strings.HasPrefix(name, "weknora_x03_") {
					m.close()
					return fmt.Errorf("profile generation requires a fresh isolated weknora_x03_ database")
				}
				if _, err := m.conn.ExecContext(
					ctx,
					"SELECT set_config('app.skip_embedding',$1,false)",
					strconv.FormatBool(variant == "plain"),
				); err != nil {
					m.close()
					return err
				}
			}
			for _, file := range files.chain(dialect, "official") {
				if _, err := m.conn.ExecContext(ctx, file.SQL); err != nil {
					m.close()
					return fmt.Errorf("profile %s/%s %s: %w", dialect, variant, file.Path, err)
				}
				objects, _, err := describeSchema(ctx, m.conn, dialect)
				if err != nil {
					m.close()
					return err
				}
				p.Official[profileKey(dialect, variant, file.Version)] = objects
			}
			if variant == "plain" {
				for _, file := range files.chain(dialect, "topic3") {
					if _, err := m.conn.ExecContext(ctx, file.SQL); err != nil {
						m.close()
						return fmt.Errorf("topic profile %s: %w", file.Path, err)
					}
					objects, _, err := describeSchema(ctx, m.conn, dialect)
					if err != nil {
						m.close()
						return err
					}
					topic := schemaObjects{}
					for key, value := range objects {
						if topicTable(dialect, tableOfObject(key)) {
							topic[key] = value
						}
					}
					p.Topic3[profileKey(dialect, "topic3", file.Version)] = topic
				}
			}
			m.close()
		}
	}
	if len(runtimeSource) != 1 {
		return fmt.Errorf("profile generation requires the actual SQLite retriever initializer")
	}
	p.SQLiteRuntime, err = generateSQLiteRuntimeProfiles(ctx, runtimeSource[0])
	if err != nil {
		return err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	if _, err := io.Copy(gz, bytes.NewReader(data)); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return os.WriteFile(destination, buffer.Bytes(), 0o644)
}

var schemaJSONOption = regexp.MustCompile("\\b(?:text_fields|json_fields|numeric_fields|boolean_fields|datetime_fi" +
	"elds)='(?:[^']|'')*'")

func normalizeSchemaDefinition(value string) string {
	return schemaJSONOption.ReplaceAllStringFunc(value, func(option string) string {
		parts := strings.SplitN(option, "=", 2)
		raw := strings.ReplaceAll(parts[1][1:len(parts[1])-1], "''", "'")
		var decoded any
		if json.Unmarshal([]byte(raw), &decoded) != nil {
			return option
		}
		encoded, err := json.Marshal(decoded)
		if err != nil {
			return option
		}
		return parts[0] + "='" + strings.ReplaceAll(string(encoded), "'", "''") + "'"
	})
}

func hashSchema(objects schemaObjects) string {
	normalized := schemaObjects{}
	for key, value := range objects {
		normalized[key] = normalizeSchemaDefinition(value)
	}
	return hashJSON(normalized)
}

// Official SQL checks some object names across the database. A new database,
// rather than another schema, preserves those SQL semantics for each fixture.
func createIsolatedMigrationDatabase(ctx context.Context, dsn, purpose string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "", fmt.Errorf("isolated migration fixtures require a PostgreSQL URL")
	}
	if !strings.HasPrefix(strings.TrimPrefix(u.Path, "/"), "weknora_x03_") {
		return "", fmt.Errorf("fixture administration requires a weknora_x03_ database")
	}
	for _, c := range purpose {
		if (c < 'a' || c > 'z') && c != '_' {
			return "", fmt.Errorf("invalid fixture purpose")
		}
	}
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		return "", err
	}
	defer func() { _ = admin.Close() }()
	name := fmt.Sprintf("weknora_x03_%s_%d", purpose, time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+quoted(name)+" TEMPLATE template0"); err != nil {
		return "", err
	}
	u.Path = "/" + name
	q := u.Query()
	q.Del("search_path")
	u.RawQuery = q.Encode()
	return u.String(), nil
}
