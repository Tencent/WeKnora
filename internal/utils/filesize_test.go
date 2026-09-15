package utils

import "testing"

func TestGetMaxSkillBundleSizeMB(t *testing.T) {
	t.Run("defaults above the knowledge upload cap", func(t *testing.T) {
		t.Setenv("MAX_FILE_SIZE_MB", "")
		t.Setenv("MAX_SKILL_BUNDLE_SIZE_MB", "")
		if got := GetMaxSkillBundleSizeMB(); got != defaultMaxSkillBundleSizeMB {
			t.Fatalf("GetMaxSkillBundleSizeMB() = %d, want %d", got, defaultMaxSkillBundleSizeMB)
		}
		if GetMaxFileSizeMB() != defaultMaxFileSizeMB {
			t.Fatalf("knowledge cap must stay %d MB", defaultMaxFileSizeMB)
		}
	})

	t.Run("honours an explicit skill cap above the knowledge cap", func(t *testing.T) {
		t.Setenv("MAX_FILE_SIZE_MB", "50")
		t.Setenv("MAX_SKILL_BUNDLE_SIZE_MB", "300")
		if got := GetMaxSkillBundleSizeMB(); got != 300 {
			t.Fatalf("GetMaxSkillBundleSizeMB() = %d, want 300", got)
		}
	})

	t.Run("never below the knowledge upload cap", func(t *testing.T) {
		t.Setenv("MAX_FILE_SIZE_MB", "200")
		t.Setenv("MAX_SKILL_BUNDLE_SIZE_MB", "64")
		if got := GetMaxSkillBundleSizeMB(); got != 200 {
			t.Fatalf("GetMaxSkillBundleSizeMB() = %d, want 200", got)
		}
	})

	t.Run("caps at the uncompressed archive ceiling", func(t *testing.T) {
		t.Setenv("MAX_FILE_SIZE_MB", "50")
		t.Setenv("MAX_SKILL_BUNDLE_SIZE_MB", "4096")
		if got := GetMaxSkillBundleSizeMB(); got != maxSkillBundleSizeMBCeiling {
			t.Fatalf("GetMaxSkillBundleSizeMB() = %d, want %d", got, maxSkillBundleSizeMBCeiling)
		}
	})
}

func TestGetMaxFileURLSizeMB(t *testing.T) {
	t.Run("follows MAX_FILE_SIZE_MB when MAX_FILE_URL_SIZE_MB is unset", func(t *testing.T) {
		t.Setenv("MAX_FILE_SIZE_MB", "200")
		t.Setenv("MAX_FILE_URL_SIZE_MB", "")
		if got := GetMaxFileURLSizeMB(); got != 200 {
			t.Fatalf("GetMaxFileURLSizeMB() = %d, want 200", got)
		}
		if got := GetMaxFileURLSize(); got != 200*1024*1024 {
			t.Fatalf("GetMaxFileURLSize() = %d, want %d", got, int64(200*1024*1024))
		}
	})

	t.Run("defaults to the knowledge upload cap", func(t *testing.T) {
		t.Setenv("MAX_FILE_SIZE_MB", "")
		t.Setenv("MAX_FILE_URL_SIZE_MB", "")
		if got := GetMaxFileURLSizeMB(); got != defaultMaxFileSizeMB {
			t.Fatalf("GetMaxFileURLSizeMB() = %d, want %d", got, defaultMaxFileSizeMB)
		}
	})

	t.Run("honours an explicit URL cap independent of MAX_FILE_SIZE_MB", func(t *testing.T) {
		t.Setenv("MAX_FILE_SIZE_MB", "200")
		t.Setenv("MAX_FILE_URL_SIZE_MB", "10")
		if got := GetMaxFileURLSizeMB(); got != 10 {
			t.Fatalf("GetMaxFileURLSizeMB() = %d, want 10", got)
		}
	})

	t.Run("ignores non-positive or invalid values", func(t *testing.T) {
		t.Setenv("MAX_FILE_SIZE_MB", "50")
		t.Setenv("MAX_FILE_URL_SIZE_MB", "0")
		if got := GetMaxFileURLSizeMB(); got != 50 {
			t.Fatalf("GetMaxFileURLSizeMB() = %d, want 50 (zero rejected)", got)
		}
		t.Setenv("MAX_FILE_URL_SIZE_MB", "not-a-number")
		if got := GetMaxFileURLSizeMB(); got != 50 {
			t.Fatalf("GetMaxFileURLSizeMB() = %d, want 50 (invalid rejected)", got)
		}
	})
}
