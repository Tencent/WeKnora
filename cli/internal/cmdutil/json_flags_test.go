package cmdutil

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func TestAddJSONFlags_RegistersFlags(t *testing.T) {
	c := &cobra.Command{Use: "x"}
	var exp Exporter
	AddJSONFlags(c, &exp, []string{"id", "name"})
	assert.NotNil(t, c.Flags().Lookup("json"))
	assert.NotNil(t, c.Flags().Lookup("jq"))
	assert.NotNil(t, c.Flags().Lookup("template"))
	assert.NotNil(t, exp, "exporter must be initialized")
}
