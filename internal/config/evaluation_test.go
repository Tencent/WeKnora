package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/spf13/viper"
)

func TestConfigExposesEvaluationTaskTimeout(t *testing.T) {
	configType := reflect.TypeOf(Config{})
	evaluationField, ok := configType.FieldByName("Evaluation")
	if !ok {
		t.Fatal("Config has no Evaluation field")
	}
	if evaluationField.Tag.Get("yaml") != "evaluation" {
		t.Fatalf("Config Evaluation yaml tag = %q, want evaluation", evaluationField.Tag.Get("yaml"))
	}
	if evaluationField.Type.Kind() != reflect.Pointer {
		t.Fatalf("Config Evaluation kind = %v, want pointer", evaluationField.Type.Kind())
	}
	timeoutField, ok := evaluationField.Type.Elem().FieldByName("TaskTimeout")
	if !ok {
		t.Fatal("EvaluationConfig has no TaskTimeout field")
	}
	if timeoutField.Tag.Get("yaml") != "task_timeout" {
		t.Fatalf("EvaluationConfig TaskTimeout yaml tag = %q, want task_timeout", timeoutField.Tag.Get("yaml"))
	}
	if timeoutField.Type != reflect.TypeOf(time.Duration(0)) {
		t.Fatalf("EvaluationConfig TaskTimeout type = %v, want time.Duration", timeoutField.Type)
	}
}

func TestEvaluationTaskTimeoutUsesSafeDefault(t *testing.T) {
	tests := []struct {
		name string
		cfg  *Config
		want time.Duration
	}{
		{name: "nil config", want: DefaultEvaluationTaskTimeout},
		{name: "nil section", cfg: &Config{}, want: DefaultEvaluationTaskTimeout},
		{
			name: "zero duration",
			cfg:  &Config{Evaluation: &EvaluationConfig{}},
			want: DefaultEvaluationTaskTimeout,
		},
		{
			name: "negative duration",
			cfg:  &Config{Evaluation: &EvaluationConfig{TaskTimeout: -time.Second}},
			want: DefaultEvaluationTaskTimeout,
		},
		{
			name: "configured duration",
			cfg:  &Config{Evaluation: &EvaluationConfig{TaskTimeout: 3 * time.Hour}},
			want: 3 * time.Hour,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := EvaluationTaskTimeout(test.cfg); got != test.want {
				t.Fatalf("EvaluationTaskTimeout() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestApplyEvaluationEnvOverrides(t *testing.T) {
	tests := []struct {
		name       string
		initial    time.Duration
		nilSection bool
		env        string
		want       time.Duration
	}{
		{name: "default", nilSection: true, want: DefaultEvaluationTaskTimeout},
		{name: "configured", initial: 3 * time.Hour, want: 3 * time.Hour},
		{name: "environment override", initial: 3 * time.Hour, env: "45m", want: 45 * time.Minute},
		{name: "invalid environment", initial: 3 * time.Hour, env: "invalid", want: 3 * time.Hour},
		{name: "zero environment", nilSection: true, env: "0s", want: DefaultEvaluationTaskTimeout},
		{name: "negative environment", initial: 3 * time.Hour, env: "-1m", want: 3 * time.Hour},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("WEKNORA_EVALUATION_TASK_TIMEOUT", test.env)
			cfg := &Config{}
			if !test.nilSection {
				cfg.Evaluation = &EvaluationConfig{TaskTimeout: test.initial}
			}
			applyEvaluationEnvOverrides(cfg)
			if cfg.Evaluation == nil {
				t.Fatal("Evaluation config is nil after applying defaults")
			}
			if cfg.Evaluation.TaskTimeout != test.want {
				t.Fatalf("Evaluation task timeout = %v, want %v", cfg.Evaluation.TaskTimeout, test.want)
			}
		})
	}
}

func TestLoadConfigEvaluationTaskTimeout(t *testing.T) {
	originalWorkingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() error = %v", err)
	}
	repositoryRoot := filepath.Clean(filepath.Join(originalWorkingDirectory, "..", ".."))
	if err := os.Chdir(repositoryRoot); err != nil {
		t.Fatalf("os.Chdir(%q) error = %v", repositoryRoot, err)
	}
	originalResolvedConfigDir := resolvedConfigDir
	t.Cleanup(func() {
		viper.Reset()
		resolvedConfigDir = originalResolvedConfigDir
		if err := os.Chdir(originalWorkingDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	t.Setenv("WEKNORA_EVALUATION_TASK_TIMEOUT", "")
	viper.Reset()
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if loaded := viper.GetDuration("evaluation.task_timeout"); loaded != 2*time.Hour {
		t.Fatalf("Viper evaluation.task_timeout = %v, want 2h", loaded)
	}
	if cfg.Evaluation == nil || cfg.Evaluation.TaskTimeout != 2*time.Hour {
		t.Fatalf("YAML evaluation task timeout = %v, want 2h", EvaluationTaskTimeout(cfg))
	}

	if err := os.Setenv("WEKNORA_EVALUATION_TASK_TIMEOUT", "45m"); err != nil {
		t.Fatalf("os.Setenv() error = %v", err)
	}
	viper.Reset()
	cfg, err = LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() with environment override error = %v", err)
	}
	if cfg.Evaluation == nil || cfg.Evaluation.TaskTimeout != 45*time.Minute {
		t.Fatalf("environment evaluation task timeout = %v, want 45m", EvaluationTaskTimeout(cfg))
	}
}
