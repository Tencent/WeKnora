package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLooksLikeSVG(t *testing.T) {
	for _, in := range []string{
		`<svg xmlns="http://www.w3.org/2000/svg"></svg>`,
		"  \n<svg/>",
		"\ufeff<?xml version=\"1.0\"?>\n<!-- brand mark --><svg >x</svg>",
		"<?xml version=\"1.0\"?><!DOCTYPE svg PUBLIC \"-//W3C//DTD SVG 1.1//EN\" \"x.dtd\"><svg>y</svg>",
		"<SVG></SVG>",
	} {
		assert.True(t, looksLikeSVG([]byte(in)), "want SVG: %q", in)
	}
	for _, in := range []string{
		"", "   ", "<svg", "<svgx>", "not markup at all",
		"<html><svg></svg></html>", "<?xml version=\"1.0\"?>", "<!-- unterminated",
		"root:x:0:0::/root:/bin/sh",
	} {
		assert.False(t, looksLikeSVG([]byte(in)), "want not SVG: %q", in)
	}
}
