package cmdutil

import (
	"github.com/spf13/cobra"
)

// AddJSONFlags registers the standard output triplet on cmd:
//
//	--json [fields]   JSON envelope output, optional field-projection list
//	--jq <expr>       embedded jq filter (PR-3 wires the real evaluator)
//	--template <tmpl> Go text/template (PR-3)
//
// The three flags are mutually exclusive at evaluation time; PR-3 enforces it.
// `fields` is the list of valid field names that --json [fields] may project
// against; commands pass their resource's field set (kb / doc / chunk / ...).
//
// *exporter is initialized to a JSON exporter; PR-3 swaps in jq/template
// variants based on which flag the user supplied.
func AddJSONFlags(cmd *cobra.Command, exporter *Exporter, fields []string) {
	_ = fields // PR-3 uses this for --json field-list completion + validation
	var jsonOut bool
	var jqExpr string
	var tmpl string
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output JSON envelope")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "Filter output via embedded jq expression")
	cmd.Flags().StringVar(&tmpl, "template", "", "Format output via Go text/template")
	*exporter = NewJSONExporter()
}
