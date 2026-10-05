package checkpoint

import (
	"os"
	"testing"
)

func TestSaveResume(t *testing.T) {
	t.Setenv("WEKNORA_PROCESSING_CHECKPOINT_DIR", t.TempDir())
	key := Key("same-input")
	want := []float32{1, 2, 3}
	if err := Save("embedding", key, want); err != nil {
		t.Fatal(err)
	}
	var got []float32
	if !Load("embedding", key, &got) || len(got) != 3 || got[2] != 3 {
		t.Fatalf("resume %v", got)
	}
	if Load("embedding", Key("changed-input"), &got) {
		t.Fatal("reused different input")
	}
	if err := os.WriteFile(Path("embedding", key), []byte("interrupted"), 0o600); err != nil {
		t.Fatal(err)
	}
	if Load("embedding", key, &got) {
		t.Fatal("accepted partial checkpoint")
	}
}
