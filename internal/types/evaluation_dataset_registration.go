package types

// EvaluationBuiltinDatasetRegistration carries one built-in dataset bundle
// through the controlled server-side registration flow. ArtifactBytes are the
// raw bundle bytes (for example the canonical JSON encoding of the converted
// Parquet samples); ExpectedArtifactSHA256 pins them for tamper evidence.
type EvaluationBuiltinDatasetRegistration struct {
	DatasetID              string
	Name                   string
	Description            string
	Content                *EvaluationDatasetVersionInput
	ArtifactBytes          []byte
	ExpectedArtifactSHA256 string
	Manifest               JSON
}
