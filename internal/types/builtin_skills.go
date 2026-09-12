package types

// BuiltinSkillsManifest is published with a tested image, independently of any
// workspace's installed skills. Digests cover the bundled resources.
type BuiltinSkillsManifest struct {
	SchemaVersion int                       `json:"schema_version"`
	Profile       string                    `json:"profile"`
	Version       string                    `json:"version"`
	Skills        []BuiltinSkillDeclaration `json:"skills"`
}

// BuiltinSkillDeclaration pins a named skill to its verified resource digest.
type BuiltinSkillDeclaration struct {
	Name     string `json:"name"`
	Digest   string `json:"digest"`
	Verified bool   `json:"verified"`
}

// TemplateSkillsDeclaration attaches a publisher manifest to one control-plane
// revision. Changing the endpoint, provider, template or revision invalidates it.
// This is used when a provider does not expose OCI image labels.
type TemplateSkillsDeclaration struct {
	Provider   string                `json:"provider"`
	Endpoint   string                `json:"endpoint"`
	TemplateID string                `json:"template_id"`
	Revision   string                `json:"revision"`
	Manifest   BuiltinSkillsManifest `json:"manifest"`
}
