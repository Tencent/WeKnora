package types

import "testing"

func TestContainsStorageReference(t *testing.T) {
	for _, tt := range []struct {
		name, text, reference string
		want                  bool
	}{
		{"markdown", "![image](local://7/exports/chart.png)", "local://7/exports/chart.png", true},
		{"path prefix", "![image](local://7/exports/chart.png.bak)", "local://7/exports/chart.png", false},
		{
			"nested image metadata",
			`{"image_info":"[{\"url\":\"local://7/exports/chart.png\"}]"}`,
			"local://7/exports/chart.png",
			true,
		},

		{
			"escaped handle suffix",
			`{"url":"resource://AbCdEfGhIjKlMnOpQrStUv\u0058"}`,
			"resource://AbCdEfGhIjKlMnOpQrStUv",
			false,
		},

		{"empty reference", `{"url":"local://7/exports/chart.png"}`, "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := ContainsStorageReference(tt.text, tt.reference); got != tt.want {
				t.Fatalf("ContainsStorageReference = %v, want %v", got, tt.want)
			}
		})
	}
}
