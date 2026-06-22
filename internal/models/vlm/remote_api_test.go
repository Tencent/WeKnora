package vlm

import "testing"

func TestNewRemoteAPIVLM_FixedTempOne(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"kimi-k2.6", true},
		{"kimi-k2.5", true},
		{"moonshot-v1-8k-vision-preview", true},
		{"kimi-k2", false},
		{"kimi-k2-turbo-preview", false},
		{"gpt-4o", false},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			v, err := NewRemoteAPIVLM(&Config{
				ModelName: tc.model,
				BaseURL:   "https://api.example.com/v1",
				APIKey:    "sk",
				Provider:  "moonshot",
			})
			if err != nil {
				t.Fatalf("NewRemoteAPIVLM: %v", err)
			}
			if v.fixedTempOne != tc.want {
				t.Errorf("fixedTempOne for %q = %v, want %v", tc.model, v.fixedTempOne, tc.want)
			}
		})
	}
}
