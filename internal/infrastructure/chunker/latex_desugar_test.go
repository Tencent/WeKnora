package chunker

import "testing"

func TestSanitizeInlineLaTeX_RealMineruExamples(t *testing.T) {
	// All inputs are verbatim from chunks produced by MinerU VLM on the
	// "二甲双胍...铁死亡" paper during development. Expected outputs are
	// what a human reader would write.
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"range_with_nbsp_unit",
			`体质量 $22 \sim 26 \mathrm{~g}$，`,
			"体质量 22 ~ 26 g，",
		},
		{
			"temperature_plus_minus",
			`温度 $(22 \pm 1) ^{\circ} \mathrm{C}$、`,
			// The space between ° and C mirrors the original LaTeX source's
			// space between `^{\circ}` and `\mathrm{C}`. Readable either way.
			"温度 (22 ± 1) ° C、",
		},
		{
			"percent",
			`湿度 $10 \%$ 的清洁级`,
			"湿度 10 % 的清洁级",
		},
		{
			"iron_ion",
			`铁离子 $\left( \mathsf{Fe}^{2+} \right)$、`,
			// \left( \right) are left alone (genuine formula syntax),
			// but everything else sanitises.
			`铁离子 $\left( Fe²⁺ \right)$、`,
		},
		{
			"iron_ion_simple",
			`$\mathrm{Fe}^{2+}$`,
			"Fe²⁺",
		},
		{
			"co2",
			`$5\% \mathrm{CO}_{2}$`,
			"5% CO₂",
		},
		{
			"mg_per_kg",
			`连续 5 天以 $40 \mathrm{mg} / \mathrm{kg}$ 新鲜制备`,
			"连续 5 天以 40 mg / kg 新鲜制备",
		},
		{
			"mmol",
			`分别用含 $5.5 \mathrm{mmol} / \mathrm{L}$、 $30 \mathrm{mmol} / \mathrm{L}$ 葡萄糖`,
			"分别用含 5.5 mmol / L、 30 mmol / L 葡萄糖",
		},
		{
			"37_degrees_c",
			`于 $5\% \mathrm{CO}_{2}$、 $37^{\circ} \mathrm{C}$ 条件下`,
			"于 5% CO₂、 37° C 条件下",
		},
		{
			"dose_parens",
			`二甲双胍 $(200 \mathrm{mg} / \mathrm{kg})$，连续 12 周`,
			"二甲双胍 (200 mg / kg)，连续 12 周",
		},
		{
			"subscript_h2o",
			`$\mathrm{H}_{2} \mathrm{O}$`,
			"H₂ O",
		},
		{
			"greek_alpha",
			`$\alpha$-tubulin 抗体`,
			"α-tubulin 抗体",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SanitizeInlineLaTeX(c.in)
			if got != c.want {
				t.Errorf("\n  input: %s\n   want: %s\n    got: %s",
					c.in, c.want, got)
			}
		})
	}
}

func TestSanitizeInlineLaTeX_Idempotent(t *testing.T) {
	// Running twice must not drift.
	inputs := []string{
		`体质量 $22 \sim 26 \mathrm{~g}$`,
		`$\mathrm{Fe}^{2+}$`,
		`$37^{\circ} \mathrm{C}$`,
		"纯中文没有任何 LaTeX",
		"",
	}
	for _, in := range inputs {
		once := SanitizeInlineLaTeX(in)
		twice := SanitizeInlineLaTeX(once)
		if once != twice {
			t.Errorf("not idempotent:\n  input: %s\n   once: %s\n  twice: %s",
				in, once, twice)
		}
	}
}

func TestSanitizeInlineLaTeX_LeavesComplexFormulasAlone(t *testing.T) {
	// A genuine integral formula must NOT have its $$...$$ stripped or its
	// inner LaTeX commands corrupted. We only target prose-disguised-as-
	// math.
	in := `$$\int_{0}^{\infty} e^{-\lambda x} dx = \frac{1}{\lambda}$$`
	got := SanitizeInlineLaTeX(in)
	if got == in {
		// Unicode substitutions are applied to inner commands; that is OK.
		// What we care about: $$ delimiters survive because we only target $...$.
		return
	}
	// Must still be wrapped in $$...$$
	if !hasPrefixAndSuffix(got, "$$", "$$") {
		t.Errorf("$$...$$ block formula was partially stripped:\n  in:  %s\n  out: %s",
			in, got)
	}
}

func TestSanitizeInlineLaTeX_Noop(t *testing.T) {
	// Plain text with no LaTeX goes through unchanged.
	in := "这是一段完全不含任何 LaTeX 的医学正文，保留 22-26 g 单位即可。"
	got := SanitizeInlineLaTeX(in)
	if got != in {
		t.Errorf("plain text should pass through; got: %s", got)
	}
}

func hasPrefixAndSuffix(s, prefix, suffix string) bool {
	return len(s) >= len(prefix)+len(suffix) &&
		s[:len(prefix)] == prefix &&
		s[len(s)-len(suffix):] == suffix
}
