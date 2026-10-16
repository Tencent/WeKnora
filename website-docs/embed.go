// Package websitedocs embeds the published documentation pages so the server
// can read the docs of its own version. Only the pages under the numbered
// sections are embedded; the site build, homepage and sample data are not.
package websitedocs

import "embed"

// Pages holds the published Markdown pages, named "<section>/<page>.md".
//
//go:embed 01-getting-started/*.md 02-architecture/*.md 03-features/*.md 04-api/*.md
//go:embed 05-clients/*.md 06-development/*.md 07-releases/*.md
var Pages embed.FS
