package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/asr"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func TestValidateWorkerConcurrencyMinimums(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   any
		wantErr bool
	}{
		{name: "core zero", key: "asynq.core_concurrency", value: 0, wantErr: true},
		{name: "core minimum", key: "asynq.core_concurrency", value: 1},
		{name: "postprocess minimum", key: "asynq.postprocess_concurrency", value: 1},
		{name: "enrichment minimum", key: "asynq.enrichment_concurrency", value: 1},
		{name: "maintenance minimum", key: "asynq.maintenance_concurrency", value: 1},
		{name: "shared minimum", key: "asynq.shared_concurrency", value: 1},
		{name: "wiki zero", key: "asynq.wiki_concurrency", value: 0, wantErr: true},
		{name: "wiki minimum", key: "asynq.wiki_concurrency", value: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRegistryEntry(tt.key, tt.value)
			if tt.wantErr && err == nil {
				t.Fatal("expected validation error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}

type registrationSettingRepo struct {
	interfaces.SystemSettingRepository
	row *types.SystemSetting
}

func (r *registrationSettingRepo) Get(context.Context, string) (*types.SystemSetting, error) {
	return r.row, nil
}

func (r *registrationSettingRepo) Upsert(_ context.Context, row *types.SystemSetting) error {
	r.row = row
	return nil
}

func TestValidateRegistrationModes(t *testing.T) {
	svc := &systemSettingService{repo: &registrationSettingRepo{}, cache: make(map[string]*types.SystemSetting)}
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "system-admin")
	for _, mode := range []string{"self_serve", "invite_register", "invite_only"} {
		row, err := svc.Update(ctx, "auth.registration_mode", mode)
		if err != nil {
			t.Fatalf("mode %q rejected: %v", mode, err)
		}
		if len(row.Enum) != 3 || svc.GetString(ctx, "auth.registration_mode", "", "") != mode {
			t.Fatalf("saved mode %q must be effective and expose all three choices", mode)
		}
	}
	if _, err := svc.Update(ctx, "auth.registration_mode", "unknown"); err == nil {
		t.Fatal("unknown registration mode accepted")
	}
}

// The ASR chunking keys are registered with the documented defaults, category
// and ENV fallback; both are read by the cutter on every transcription, so
// neither requires a restart.
func TestASRChunkingKeysAreRegistered(t *testing.T) {
	enabledSpec, ok := registry[asr.LongAudioChunkingSettingKey]
	if !ok {
		t.Fatalf("%q is not registered", asr.LongAudioChunkingSettingKey)
	}
	if enabledSpec.Type != "bool" || enabledSpec.Default != false {
		t.Fatalf("switch spec = %+v, want a bool defaulting to false", enabledSpec)
	}
	if enabledSpec.Category != "asr" {
		t.Fatalf("switch category = %q, want asr", enabledSpec.Category)
	}

	segmentSpec, ok := registry[asr.MaxSegmentSecondsSettingKey]
	if !ok {
		t.Fatalf("%q is not registered", asr.MaxSegmentSecondsSettingKey)
	}
	if segmentSpec.Type != "int" || segmentSpec.Default != int64(asr.DefaultMaxSegmentSeconds) {
		t.Fatalf("segment spec = %+v, want an int defaulting to %d",
			segmentSpec, asr.DefaultMaxSegmentSeconds)
	}
	if segmentSpec.EnvName != asr.MaxSegmentSecondsEnvName {
		t.Fatalf("segment env = %q, want %q", segmentSpec.EnvName, asr.MaxSegmentSecondsEnvName)
	}
	if segmentSpec.Category != "asr" {
		t.Fatalf("segment category = %q, want asr", segmentSpec.Category)
	}
}

// The segment ceiling is validated at the boundary so a typo cannot persist a
// value the cutter would refuse (or one that would flood the vendor).
func TestValidateASRMaxSegmentSeconds(t *testing.T) {
	tests := []struct {
		name    string
		value   any
		wantErr bool
	}{
		{name: "below the floor", value: 29, wantErr: true},
		{name: "minimum", value: 30},
		{name: "default", value: 120},
		{name: "maximum", value: 300},
		{name: "above the ceiling", value: 301, wantErr: true},
		{name: "not an integer", value: "two minutes", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRegistryEntry(asr.MaxSegmentSecondsSettingKey, tt.value)
			if tt.wantErr && err == nil {
				t.Fatal("expected validation error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}

// memorySettingRepo keeps one row per key, so a test can exercise both ASR
// keys at once.
type memorySettingRepo struct {
	interfaces.SystemSettingRepository
	rows map[string]*types.SystemSetting
	err  error
}

func (r *memorySettingRepo) Get(_ context.Context, key string) (*types.SystemSetting, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.rows[key], nil
}

func (r *memorySettingRepo) Upsert(_ context.Context, row *types.SystemSetting) error {
	if r.err != nil {
		return r.err
	}
	r.rows[row.Key] = row
	return nil
}

// The switch defaults to off and a broken settings store degrades to that
// default instead of failing the transcription path.
func TestASRChunkingSettingsDefaultOffAndDegrade(t *testing.T) {
	t.Setenv(asr.MaxSegmentSecondsEnvName, "")
	svc := &systemSettingService{
		repo:  &memorySettingRepo{err: errors.New("db unavailable")},
		cache: make(map[string]*types.SystemSetting),
	}
	ctx := context.Background()

	if svc.GetBool(ctx, asr.LongAudioChunkingSettingKey, "", false) {
		t.Fatal("a failed read must fall back to the disabled default")
	}
	got := svc.GetInt(ctx, asr.MaxSegmentSecondsSettingKey, asr.MaxSegmentSecondsEnvName,
		int64(asr.DefaultMaxSegmentSeconds))
	if got != int64(asr.DefaultMaxSegmentSeconds) {
		t.Fatalf("segment seconds = %d, want the default %d", got, asr.DefaultMaxSegmentSeconds)
	}
}

// An admin edit is persisted and immediately visible through the resolver the
// cutter reads, which is what makes both knobs runtime-tunable.
func TestASRChunkingSettingsUpdateRoundTrip(t *testing.T) {
	t.Setenv(asr.MaxSegmentSecondsEnvName, "")
	svc := &systemSettingService{
		repo:  &memorySettingRepo{rows: make(map[string]*types.SystemSetting)},
		cache: make(map[string]*types.SystemSetting),
	}
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "system-admin")

	if _, err := svc.Update(ctx, asr.LongAudioChunkingSettingKey, true); err != nil {
		t.Fatalf("enable switch: %v", err)
	}
	if _, err := svc.Update(ctx, asr.MaxSegmentSecondsSettingKey, 45); err != nil {
		t.Fatalf("set segment seconds: %v", err)
	}
	if !svc.GetBool(ctx, asr.LongAudioChunkingSettingKey, "", false) {
		t.Fatal("saved switch is not effective")
	}
	if got := svc.GetInt(ctx, asr.MaxSegmentSecondsSettingKey, asr.MaxSegmentSecondsEnvName,
		int64(asr.DefaultMaxSegmentSeconds)); got != 45 {
		t.Fatalf("segment seconds = %d, want 45", got)
	}
	if _, err := svc.Update(ctx, asr.MaxSegmentSecondsSettingKey, 301); err == nil {
		t.Fatal("an out-of-range segment length must be rejected")
	}
}

// The legacy ENV value is only the fallback: a row saved in the management UI
// must win, which is what lets an operator retune the ceiling without a
// redeploy.
func TestASRMaxSegmentOverridesEnvDefault(t *testing.T) {
	t.Setenv(asr.MaxSegmentSecondsEnvName, "90")
	svc := &systemSettingService{
		repo:  &memorySettingRepo{rows: make(map[string]*types.SystemSetting)},
		cache: make(map[string]*types.SystemSetting),
	}
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "system-admin")
	read := func() int64 {
		return svc.GetInt(ctx, asr.MaxSegmentSecondsSettingKey, asr.MaxSegmentSecondsEnvName,
			int64(asr.DefaultMaxSegmentSeconds))
	}

	if got := read(); got != 90 {
		t.Fatalf("segment seconds = %d, want the ENV fallback 90", got)
	}
	if _, err := svc.Update(ctx, asr.MaxSegmentSecondsSettingKey, 45); err != nil {
		t.Fatalf("set segment seconds: %v", err)
	}
	if got := read(); got != 45 {
		t.Fatalf("segment seconds = %d, want the saved value 45 to override the ENV", got)
	}
}
