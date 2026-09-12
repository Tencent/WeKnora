// Package dataset exposes version-controlled evaluation dataset artifacts.
package dataset

import "embed"

// Samples contains the five Parquet files that define the built-in dataset.
//
//go:embed samples/*.parquet
var Samples embed.FS
