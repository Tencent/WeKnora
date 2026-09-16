package config

import (
	"testing"
)

func TestEvaluationRetentionDaysResolvesPointerSemantics(t *testing.T) {
	// Unset configuration means the 90-day default.
	days, enabled, err := EvaluationRetentionDays(&Config{})
	if err != nil || !enabled || days != DefaultEvaluationRetentionDays {
		t.Fatalf("default resolution = (%d, %v, %v), want (90, true, nil)", days, enabled, err)
	}
	days, enabled, err = EvaluationRetentionDays(&Config{Evaluation: &EvaluationConfig{}})
	if err != nil || !enabled || days != DefaultEvaluationRetentionDays {
		t.Fatalf("nil pointer resolution = (%d, %v, %v), want (90, true, nil)", days, enabled, err)
	}

	// Explicit zero disables the cleanup.
	zero := 0
	days, enabled, err = EvaluationRetentionDays(&Config{Evaluation: &EvaluationConfig{RetentionDays: &zero}})
	if err != nil || enabled || days != 0 {
		t.Fatalf("zero resolution = (%d, %v, %v), want (0, false, nil)", days, enabled, err)
	}

	// Negative values are startup errors.
	negative := -1
	_, _, err = EvaluationRetentionDays(&Config{Evaluation: &EvaluationConfig{RetentionDays: &negative}})
	if err == nil {
		t.Fatal("negative retention_days must fail startup")
	}

	// Positive values are used as-is.
	thirty := 30
	days, enabled, err = EvaluationRetentionDays(&Config{Evaluation: &EvaluationConfig{RetentionDays: &thirty}})
	if err != nil || !enabled || days != 30 {
		t.Fatalf("positive resolution = (%d, %v, %v), want (30, true, nil)", days, enabled, err)
	}
}

func TestEvaluationRetentionDaysEnvOverride(t *testing.T) {
	t.Setenv("WEKNORA_EVALUATION_RETENTION_DAYS", "45")
	cfg := &Config{}
	applyEvaluationEnvOverrides(cfg)
	if cfg.Evaluation.RetentionDays == nil || *cfg.Evaluation.RetentionDays != 45 {
		t.Fatalf("env override = %v, want 45", cfg.Evaluation.RetentionDays)
	}

	// An invalid override keeps the yaml value.
	thirty := 30
	t.Setenv("WEKNORA_EVALUATION_RETENTION_DAYS", "not-a-number")
	cfg = &Config{Evaluation: &EvaluationConfig{RetentionDays: &thirty}}
	applyEvaluationEnvOverrides(cfg)
	if cfg.Evaluation.RetentionDays == nil || *cfg.Evaluation.RetentionDays != 30 {
		t.Fatalf("invalid env override must keep yaml value, got %v", cfg.Evaluation.RetentionDays)
	}
}
