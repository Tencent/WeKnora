package database

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"text/template"

	"gopkg.in/yaml.v3"
)

func TestComposeMySQLOverlayStaticSemantics(t *testing.T) {
	root := filepath.Join("..", "..")
	base := readYAMLRoot(t, filepath.Join(root, "docker-compose.yml"))
	overlay := readYAMLRoot(t, filepath.Join(root, "docker-compose.mysql.yml"))

	baseServices := yamlMapValue(t, base, "services")
	overlayServices := yamlMapValue(t, overlay, "services")
	baseApp := yamlMapValue(t, baseServices, "app")
	overlayApp := yamlMapValue(t, overlayServices, "app")

	baseEnvFile := yamlMapValue(t, baseApp, "env_file")
	if baseEnvFile.Kind != yaml.SequenceNode || len(baseEnvFile.Content) != 1 ||
		baseEnvFile.Content[0].Value != ".env" {
		t.Fatalf("base app env_file must retain the documented .env workflow")
	}
	if yamlMapValueOptional(overlayApp, "env_file") != nil {
		t.Fatal("mysql overlay must not claim it can make the base .env optional during Compose merge")
	}

	dependsOn := yamlMapValue(t, overlayApp, "depends_on")
	if dependsOn.Tag != "!override" {
		t.Fatalf(
			"mysql app depends_on tag = %q, want !override so PostgreSQL is removed during Compose merge",
			dependsOn.Tag,
		)
	}
	for _, dependency := range []string{"redis", "mysql", "docreader"} {
		if yamlMapValueOptional(dependsOn, dependency) == nil {
			t.Errorf("mysql app depends_on missing %q", dependency)
		}
	}
	if yamlMapValueOptional(dependsOn, "postgres") != nil {
		t.Fatal("mysql app still depends on postgres")
	}

	postgres := yamlMapValue(t, overlayServices, "postgres")
	profiles := yamlStringSequence(t, yamlMapValue(t, postgres, "profiles"))
	if len(profiles) == 0 {
		t.Fatal("postgres has no profile in the MySQL overlay and would start by default")
	}
	for _, want := range []string{"langfuse", "full"} {
		if !containsString(profiles, want) {
			t.Errorf("postgres profiles = %v, missing %q required by the optional Langfuse stack", profiles, want)
		}
	}

	envExample := parseDotEnv(t, filepath.Join(root, ".env.example"))
	scenarios := []struct {
		name          string
		vars          map[string]string
		wantRetriever string
	}{
		{name: "empty environment", vars: map[string]string{}, wantRetriever: "mysql"},
		{name: "standard PostgreSQL env file", vars: envExample, wantRetriever: "mysql"},
		{
			name:          "explicit non-SQL retriever",
			vars:          map[string]string{"MYSQL_RETRIEVE_DRIVER": "qdrant"},
			wantRetriever: "qdrant",
		},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			appEnv := yamlEnvironment(t, yamlMapValue(t, baseApp, "environment"), scenario.vars)
			for key, value := range yamlEnvironment(
				t,
				yamlMapValue(t, overlayApp, "environment"),
				scenario.vars,
			) {
				appEnv[key] = value
			}

			for key, want := range map[string]string{
				"DB_DRIVER":             "mysql",
				"DB_HOST":               "mysql",
				"DB_PORT":               "3306",
				"DB_MAX_OPEN_CONNS":     "20",
				"DB_MAX_IDLE_CONNS":     "10",
				"DB_CONN_MAX_LIFETIME":  "10m",
				"DB_CONN_MAX_IDLE_TIME": "5m",
				"RETRIEVE_DRIVER":       scenario.wantRetriever,
				"MYSQL_HOST":            "mysql",
				"MYSQL_PORT":            "3306",
			} {
				if got := appEnv[key]; got != want {
					t.Errorf("app %s = %q, want %q", key, got, want)
				}
			}
			for mysqlKey, dbKey := range map[string]string{
				"MYSQL_USERNAME": "DB_USER",
				"MYSQL_PASSWORD": "DB_PASSWORD",
				"MYSQL_DATABASE": "DB_NAME",
			} {
				if got, want := appEnv[mysqlKey], appEnv[dbKey]; got != want {
					t.Errorf("app %s = %q, want %s value %q", mysqlKey, got, dbKey, want)
				}
			}

			mysqlService := yamlMapValue(t, overlayServices, "mysql")
			mysqlEnv := yamlEnvironment(t, yamlMapValue(t, mysqlService, "environment"), scenario.vars)
			for mysqlKey, dbKey := range map[string]string{
				"MYSQL_USER":     "DB_USER",
				"MYSQL_PASSWORD": "DB_PASSWORD",
				"MYSQL_DATABASE": "DB_NAME",
			} {
				if got, want := mysqlEnv[mysqlKey], appEnv[dbKey]; got != want {
					t.Errorf("mysql service %s = %q, want app %s value %q", mysqlKey, got, dbKey, want)
				}
			}
		})
	}
}

func TestComposeMySQLOverlayWithDocker(t *testing.T) {
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("Docker Compose is unavailable; static merge-contract test still runs")
	}
	if output, err := exec.Command(docker, "compose", "version").CombinedOutput(); err != nil {
		t.Skipf("Docker Compose plugin is unavailable: %v (%s)", err, strings.TrimSpace(string(output)))
	}

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	composeDir := t.TempDir()
	for _, name := range []string{"docker-compose.yml", "docker-compose.mysql.yml"} {
		body, readErr := os.ReadFile(filepath.Join(root, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if writeErr := os.WriteFile(filepath.Join(composeDir, name), body, 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	emptyEnv := filepath.Join(composeDir, ".env")
	if err := os.WriteFile(emptyEnv, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	scenarios := []struct {
		name    string
		envFile string
	}{
		{name: "empty environment", envFile: emptyEnv},
		{name: "standard PostgreSQL env file", envFile: filepath.Join(root, ".env.example")},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			args := []string{
				"compose",
				"--env-file", scenario.envFile,
				"-f", filepath.Join(composeDir, "docker-compose.yml"),
				"-f", filepath.Join(composeDir, "docker-compose.mysql.yml"),
				"config",
				"--format", "json",
			}
			cmd := exec.Command(docker, args...)
			cmd.Dir = root
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("docker compose config failed: %v\n%s", err, output)
			}

			var config struct {
				Services map[string]struct {
					Environment map[string]any `json:"environment"`
					DependsOn   map[string]any `json:"depends_on"`
					Profiles    []string       `json:"profiles"`
				} `json:"services"`
			}
			if err := json.Unmarshal(output, &config); err != nil {
				t.Fatalf("decode docker compose config: %v\n%s", err, output)
			}
			app, ok := config.Services["app"]
			if !ok {
				t.Fatal("rendered Compose config has no app service")
			}
			if _, ok := app.DependsOn["postgres"]; ok {
				t.Fatal("rendered MySQL app still depends on postgres")
			}
			if _, ok := app.DependsOn["mysql"]; !ok {
				t.Fatal("rendered MySQL app does not depend on mysql")
			}
			for key, want := range map[string]string{
				"DB_DRIVER":             "mysql",
				"DB_HOST":               "mysql",
				"DB_PORT":               "3306",
				"DB_MAX_OPEN_CONNS":     "20",
				"DB_MAX_IDLE_CONNS":     "10",
				"DB_CONN_MAX_LIFETIME":  "10m",
				"DB_CONN_MAX_IDLE_TIME": "5m",
				"RETRIEVE_DRIVER":       "mysql",
				"MYSQL_HOST":            "mysql",
				"MYSQL_PORT":            "3306",
			} {
				if got := jsonString(app.Environment[key]); got != want {
					t.Errorf("rendered app %s = %q, want %q", key, got, want)
				}
			}
			for mysqlKey, dbKey := range map[string]string{
				"MYSQL_USERNAME": "DB_USER",
				"MYSQL_PASSWORD": "DB_PASSWORD",
				"MYSQL_DATABASE": "DB_NAME",
			} {
				if got, want := jsonString(app.Environment[mysqlKey]), jsonString(app.Environment[dbKey]); got != want {
					t.Errorf("rendered app %s = %q, want %s value %q", mysqlKey, got, dbKey, want)
				}
			}
			if postgres, ok := config.Services["postgres"]; ok && len(postgres.Profiles) == 0 {
				t.Fatal("rendered postgres service has no profile and would start in the default MySQL project")
			}
		})
	}
}

func TestHelmMySQLModeStaticContract(t *testing.T) {
	root := filepath.Join("..", "..")
	values := readYAMLRoot(t, filepath.Join(root, "helm", "values.yaml"))
	database := yamlMapValue(t, values, "database")
	if got := yamlMapValue(t, database, "driver").Value; got != "postgres" {
		t.Fatalf("default database.driver = %q, want postgres", got)
	}
	mysqlValues := yamlMapValue(t, database, "mysql")
	if got := yamlMapValue(t, mysqlValues, "host").Value; got != "" {
		t.Fatalf("default database.mysql.host = %q, want empty so an external host is explicit", got)
	}
	if got := yamlMapValue(t, mysqlValues, "port").Value; got != "3306" {
		t.Fatalf("default database.mysql.port = %q, want 3306", got)
	}
	mysqlPool := yamlMapValue(t, mysqlValues, "pool")
	for key, want := range map[string]string{
		"maxOpenConns":    "20",
		"maxIdleConns":    "10",
		"connMaxLifetime": "10m",
		"connMaxIdleTime": "5m",
	} {
		if got := yamlMapValue(t, mysqlPool, key).Value; got != want {
			t.Errorf("default database.mysql.pool.%s = %q, want %q", key, got, want)
		}
	}
	appEnv := yamlMapValue(t, yamlMapValue(t, values, "app"), "env")
	if got := yamlMapValue(t, appEnv, "RETRIEVE_DRIVER").Value; got != "" {
		t.Fatalf("default RETRIEVE_DRIVER = %q, want empty so it follows database.driver", got)
	}

	helpers := readText(t, filepath.Join(root, "helm", "templates", "_helpers.tpl"))
	for _, want := range []string{
		`define "weknora.databaseDriver"`,
		`define "weknora.databaseHost"`,
		`define "weknora.databasePort"`,
		`define "weknora.retrievalDriver"`,
		`database.mysql.host is required when database.driver=mysql`,
		`cannot be combined with RETRIEVE_DRIVER`,
	} {
		if !strings.Contains(helpers, want) {
			t.Errorf("Helm database helpers missing %q", want)
		}
	}
	if strings.Contains(helpers, `(eq $driver "postgres") (has "mysql"`) {
		t.Error("Helm validation incorrectly rejects PostgreSQL main DB with an independent MySQL retriever")
	}
	validation := readText(t, filepath.Join(root, "helm", "templates", "validate.yaml"))
	if !strings.Contains(validation, `include "weknora.validateDatabase" .`) {
		t.Fatal("Helm chart does not invoke database-mode validation")
	}

	appTemplate := readText(t, filepath.Join(root, "helm", "templates", "app.yaml"))
	for _, want := range []string{
		`value: {{ include "weknora.databaseDriver" . | quote }}`,
		`value: {{ include "weknora.databaseHost" . | quote }}`,
		`value: {{ include "weknora.databasePort" . | quote }}`,
		`value: {{ include "weknora.retrievalDriver" . | quote }}`,
		`if and (eq (include "weknora.databaseDriver" .) "mysql") (has "mysql" (splitList "," (include "weknora.retrievalDriver" .)))`,
		`value: {{ .Values.database.mysql.pool.maxOpenConns | quote }}`,
	} {
		if !strings.Contains(appTemplate, want) {
			t.Errorf("Helm app template missing %q", want)
		}
	}
	for envName, secretKey := range map[string]string{
		"MYSQL_USERNAME": "DB_USER",
		"MYSQL_PASSWORD": "DB_PASSWORD",
		"MYSQL_DATABASE": "DB_NAME",
	} {
		pattern := regexp.MustCompile(
			`(?s)- name: ` + regexp.QuoteMeta(envName) + `.{0,300}?key: ` + regexp.QuoteMeta(secretKey),
		)
		if !pattern.MatchString(appTemplate) {
			t.Errorf("Helm app template does not map %s from secret key %s", envName, secretKey)
		}
	}
	mysqlPoolPattern := regexp.MustCompile(
		`(?s)if eq \(include "weknora\.databaseDriver" \.\) "mysql".{0,900}` +
			`name: DB_MAX_OPEN_CONNS.{0,700}` +
			`name: DB_CONN_MAX_IDLE_TIME.{0,300}\{\{- end \}\}`,
	)
	if !mysqlPoolPattern.MatchString(appTemplate) {
		t.Error("Helm connection-pool defaults are not confined to database.driver=mysql")
	}

	postgresGuard := `(eq (include "weknora.databaseDriver" .) "postgres")`
	for _, name := range []string{"postgres.yaml", "pvc.yaml", "NOTES.txt"} {
		body := readText(t, filepath.Join(root, "helm", "templates", name))
		if strings.Contains(body, ".Values.postgresql.enabled") && !strings.Contains(body, postgresGuard) {
			t.Errorf("%s can render PostgreSQL while database.driver=mysql", name)
		}
	}
}

func TestHelmMySQLTemplatesParse(t *testing.T) {
	root := filepath.Join("..", "..", "helm", "templates")
	stub := func(...any) any { return "" }
	booleanStub := func(...any) bool { return false }
	templates := template.New("chart").Funcs(template.FuncMap{
		"b64dec":       stub,
		"contains":     booleanStub,
		"default":      stub,
		"dict":         stub,
		"fail":         stub,
		"has":          booleanStub,
		"include":      stub,
		"list":         stub,
		"lookup":       stub,
		"lower":        stub,
		"merge":        stub,
		"nindent":      stub,
		"quote":        stub,
		"randAlphaNum": stub,
		"replace":      stub,
		"required":     stub,
		"splitList":    stub,
		"ternary":      stub,
		"toYaml":       stub,
		"trimSuffix":   stub,
		"trunc":        stub,
	})
	files, err := filepath.Glob(filepath.Join(root, "*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		if info.IsDir() {
			continue
		}
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := templates.New(filepath.Base(file)).Parse(string(body)); err != nil {
			t.Fatalf("parse Helm Go-template syntax in %s: %v", file, err)
		}
	}
}

func TestHelmMySQLModeWithHelm(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("Helm is unavailable; static template-contract test still runs")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	chart := filepath.Join(root, "helm")
	baseArgs := []string{
		"template", "audit", chart,
		"--namespace", "audit",
		"--set-string", "secrets.dbPassword=audit-db-password",
		"--set-string", "secrets.redisPassword=audit-redis-password",
		"--set-string", "secrets.jwtSecret=audit-jwt-secret",
	}

	t.Run("mysql render", func(t *testing.T) {
		args := append(append([]string{}, baseArgs...),
			"--set-string", "database.driver=mysql",
			"--set-string", "database.mysql.host=mysql.example.internal",
		)
		output, err := exec.Command(helm, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("helm template MySQL mode failed: %v\n%s", err, output)
		}
		resources := decodeYAMLDocuments(t, output)
		appEnv := renderedAppEnvironment(t, resources)
		for key, want := range map[string]string{
			"DB_DRIVER":             "mysql",
			"DB_HOST":               "mysql.example.internal",
			"DB_PORT":               "3306",
			"DB_MAX_OPEN_CONNS":     "20",
			"DB_MAX_IDLE_CONNS":     "10",
			"DB_CONN_MAX_LIFETIME":  "10m",
			"DB_CONN_MAX_IDLE_TIME": "5m",
			"RETRIEVE_DRIVER":       "mysql",
			"MYSQL_HOST":            "mysql.example.internal",
			"MYSQL_PORT":            "3306",
		} {
			if got := renderedEnvValue(appEnv[key]); got != want {
				t.Errorf("rendered Helm app %s = %q, want %q", key, got, want)
			}
		}
		for mysqlKey, secretKey := range map[string]string{
			"MYSQL_USERNAME": "DB_USER",
			"MYSQL_PASSWORD": "DB_PASSWORD",
			"MYSQL_DATABASE": "DB_NAME",
		} {
			if got := renderedSecretKey(appEnv[mysqlKey]); got != secretKey {
				t.Errorf("rendered Helm app %s secret key = %q, want %q", mysqlKey, got, secretKey)
			}
		}
		for _, resource := range resources {
			name := nestedString(resource, "metadata", "name")
			if strings.HasSuffix(name, "-postgres") {
				t.Errorf("MySQL mode rendered PostgreSQL resource %s/%s", resource["kind"], name)
			}
		}
	})

	t.Run("postgres render preserves pool defaults", func(t *testing.T) {
		output, err := exec.Command(helm, baseArgs...).CombinedOutput()
		if err != nil {
			t.Fatalf("helm template PostgreSQL mode failed: %v\n%s", err, output)
		}
		appEnv := renderedAppEnvironment(t, decodeYAMLDocuments(t, output))
		for _, key := range []string{
			"DB_MAX_OPEN_CONNS",
			"DB_MAX_IDLE_CONNS",
			"DB_CONN_MAX_LIFETIME",
			"DB_CONN_MAX_IDLE_TIME",
		} {
			if _, ok := appEnv[key]; ok {
				t.Errorf("default PostgreSQL render unexpectedly injects %s", key)
			}
		}
	})

	t.Run("postgres with independent mysql retriever is legal", func(t *testing.T) {
		args := append(append([]string{}, baseArgs...),
			"--set-string", "app.env.RETRIEVE_DRIVER=mysql",
		)
		output, err := exec.Command(helm, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("helm template PostgreSQL + MySQL retriever failed: %v\n%s", err, output)
		}
		appEnv := renderedAppEnvironment(t, decodeYAMLDocuments(t, output))
		if got := renderedEnvValue(appEnv["RETRIEVE_DRIVER"]); got != "mysql" {
			t.Fatalf("rendered RETRIEVE_DRIVER = %q, want mysql", got)
		}
		for _, key := range []string{
			"MYSQL_HOST",
			"MYSQL_PORT",
			"MYSQL_USERNAME",
			"MYSQL_PASSWORD",
			"MYSQL_DATABASE",
		} {
			if _, ok := appEnv[key]; ok {
				t.Errorf("PostgreSQL mode incorrectly derives %s from PostgreSQL connection settings", key)
			}
		}
	})

	t.Run("missing mysql host fails", func(t *testing.T) {
		args := append(append([]string{}, baseArgs...),
			"--set-string", "database.driver=mysql",
		)
		output, err := exec.Command(helm, args...).CombinedOutput()
		if err == nil || !strings.Contains(string(output), "database.mysql.host is required") {
			t.Fatalf("helm template error = %v, want required MySQL host\n%s", err, output)
		}
	})

	t.Run("cross wired SQL retriever fails", func(t *testing.T) {
		args := append(append([]string{}, baseArgs...),
			"--set-string", "database.driver=mysql",
			"--set-string", "database.mysql.host=mysql.example.internal",
			"--set-string", "app.env.RETRIEVE_DRIVER=postgres",
		)
		output, err := exec.Command(helm, args...).CombinedOutput()
		if err == nil || !strings.Contains(string(output), "cannot be combined with RETRIEVE_DRIVER") {
			t.Fatalf("helm template error = %v, want SQL driver mismatch\n%s", err, output)
		}
	})
}

func readYAMLRoot(t *testing.T, path string) *yaml.Node {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(body, &document); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		t.Fatalf("%s is not a single YAML document", path)
	}
	return document.Content[0]
}

func yamlMapValue(t *testing.T, node *yaml.Node, key string) *yaml.Node {
	t.Helper()
	value := yamlMapValueOptional(node, key)
	if value == nil {
		t.Fatalf("YAML mapping missing key %q", key)
	}
	return value
}

func yamlMapValueOptional(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return node.Content[index+1]
		}
	}
	return nil
}

func yamlStringSequence(t *testing.T, node *yaml.Node) []string {
	t.Helper()
	if node.Kind != yaml.SequenceNode {
		t.Fatalf("YAML node kind = %d, want sequence", node.Kind)
	}
	values := make([]string, 0, len(node.Content))
	for _, child := range node.Content {
		values = append(values, child.Value)
	}
	return values
}

func yamlEnvironment(t *testing.T, node *yaml.Node, variables map[string]string) map[string]string {
	t.Helper()
	result := make(map[string]string)
	switch node.Kind {
	case yaml.SequenceNode:
		for _, child := range node.Content {
			key, value, ok := strings.Cut(child.Value, "=")
			if !ok {
				t.Fatalf("environment entry %q has no '='", child.Value)
			}
			result[key] = interpolateComposeValue(value, variables)
		}
	case yaml.MappingNode:
		for index := 0; index+1 < len(node.Content); index += 2 {
			result[node.Content[index].Value] = interpolateComposeValue(node.Content[index+1].Value, variables)
		}
	default:
		t.Fatalf("environment YAML node kind = %d, want sequence or mapping", node.Kind)
	}
	return result
}

var composeDefaultPattern = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*):-([^}]*)\}$`)

func interpolateComposeValue(value string, variables map[string]string) string {
	match := composeDefaultPattern.FindStringSubmatch(value)
	if match == nil {
		return value
	}
	if resolved := variables[match[1]]; resolved != "" {
		return resolved
	}
	return match[2]
}

func parseDotEnv(t *testing.T, path string) map[string]string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	values := make(map[string]string)
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[strings.TrimSpace(key)] = value
		}
	}
	return values
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func jsonString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case nil:
		return ""
	default:
		return fmt.Sprint(typed)
	}
}

func readText(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

func decodeYAMLDocuments(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	var documents []map[string]any
	for {
		var document map[string]any
		err := decoder.Decode(&document)
		if err != nil {
			if strings.Contains(err.Error(), "EOF") {
				break
			}
			t.Fatalf("decode rendered YAML: %v", err)
		}
		if len(document) != 0 {
			documents = append(documents, document)
		}
	}
	return documents
}

func renderedAppEnvironment(t *testing.T, resources []map[string]any) map[string]map[string]any {
	t.Helper()
	for _, resource := range resources {
		if resource["kind"] != "Deployment" || !strings.HasSuffix(nestedString(resource, "metadata", "name"), "-app") {
			continue
		}
		spec := nestedMap(t, resource, "spec", "template", "spec")
		containers, ok := spec["containers"].([]any)
		if !ok || len(containers) == 0 {
			t.Fatal("rendered app Deployment has no containers")
		}
		container, ok := containers[0].(map[string]any)
		if !ok {
			t.Fatal("rendered app container is not a mapping")
		}
		entries, ok := container["env"].([]any)
		if !ok {
			t.Fatal("rendered app container has no env list")
		}
		result := make(map[string]map[string]any)
		for _, entry := range entries {
			mapping, ok := entry.(map[string]any)
			if ok {
				result[jsonString(mapping["name"])] = mapping
			}
		}
		return result
	}
	t.Fatal("rendered chart has no app Deployment")
	return nil
}

func renderedEnvValue(entry map[string]any) string {
	return jsonString(entry["value"])
}

func renderedSecretKey(entry map[string]any) string {
	valueFrom, _ := entry["valueFrom"].(map[string]any)
	secretKeyRef, _ := valueFrom["secretKeyRef"].(map[string]any)
	return jsonString(secretKeyRef["key"])
}

func nestedString(mapping map[string]any, keys ...string) string {
	current := any(mapping)
	for _, key := range keys {
		next, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = next[key]
	}
	return jsonString(current)
}

func nestedMap(t *testing.T, mapping map[string]any, keys ...string) map[string]any {
	t.Helper()
	current := mapping
	for _, key := range keys {
		next, ok := current[key].(map[string]any)
		if !ok {
			t.Fatalf("rendered YAML path %s is not a mapping", strings.Join(keys, "."))
		}
		current = next
	}
	return current
}
