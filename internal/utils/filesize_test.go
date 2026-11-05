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

func TestGetMaxBackupArchiveSizeMB(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv("MAX_FILE_SIZE_MB", "")
		t.Setenv("MAX_BACKUP_ARCHIVE_SIZE_MB", "")
		if got := GetMaxBackupArchiveSizeMB(); got != defaultMaxBackupArchiveSizeMB {
			t.Fatalf("got %d, want %d", got, defaultMaxBackupArchiveSizeMB)
		}
	})
	t.Run("never below knowledge cap", func(t *testing.T) {
		t.Setenv("MAX_FILE_SIZE_MB", "5000")
		t.Setenv("MAX_BACKUP_ARCHIVE_SIZE_MB", "100")
		if got := GetMaxBackupArchiveSizeMB(); got != 5000 {
			t.Fatalf("got %d, want 5000", got)
		}
	})
	t.Run("ceiling", func(t *testing.T) {
		t.Setenv("MAX_FILE_SIZE_MB", "50")
		t.Setenv("MAX_BACKUP_ARCHIVE_SIZE_MB", "99999")
		if got := GetMaxBackupArchiveSizeMB(); got != maxBackupArchiveSizeMBCeiling {
			t.Fatalf("got %d, want %d", got, maxBackupArchiveSizeMBCeiling)
		}
	})
}
