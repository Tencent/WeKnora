package cmdutil_test

import (
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
)

func TestAllCodes_NonEmpty(t *testing.T) {
	codes := cmdutil.AllCodes()
	if len(codes) == 0 {
		t.Fatal("AllCodes() should return registered codes")
	}
	// Sentinel: contains the baseline error codes the registry must always carry.
	want := map[cmdutil.ErrorCode]bool{
		cmdutil.CodeAuthUnauthenticated: false,
		cmdutil.CodeResourceNotFound:    false,
		cmdutil.CodeNetworkError:        false,
	}
	for _, c := range codes {
		if _, ok := want[c]; ok {
			want[c] = true
		}
	}
	for c, ok := range want {
		if !ok {
			t.Errorf("AllCodes() missing %q", c)
		}
	}
}

func TestAllCodes_NoDuplicates(t *testing.T) {
	codes := cmdutil.AllCodes()
	seen := make(map[cmdutil.ErrorCode]struct{})
	for _, c := range codes {
		if _, dup := seen[c]; dup {
			t.Errorf("AllCodes() duplicate: %q", c)
		}
		seen[c] = struct{}{}
	}
}

func TestClassifyHTTPErrorOutputs_Subset(t *testing.T) {
	outs := cmdutil.ClassifyHTTPErrorOutputs()
	all := make(map[cmdutil.ErrorCode]struct{})
	for _, c := range cmdutil.AllCodes() {
		all[c] = struct{}{}
	}
	for _, c := range outs {
		if _, ok := all[c]; !ok {
			t.Errorf("ClassifyHTTPErrorOutputs returns %q which is not in AllCodes", c)
		}
	}
	if len(outs) < 5 {
		t.Errorf("expected at least 5 codes, got %d", len(outs))
	}
}
