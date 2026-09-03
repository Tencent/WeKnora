package skills

// SkillSource is where one set of skills comes from. Two implementations
// exist: the deployment's preloaded skill directories (*Loader) and the
// projection of the skills an administrator installed into a workspace sandbox
// config's snapshot image (*TenantSkillSource).
//
// The five methods are the Progressive Disclosure levels the agent asks for:
// metadata for the system prompt, the SKILL.md body, individual resource
// files, the file listing, and the directory a script runs from.
type SkillSource interface {
	DiscoverSkills() ([]*SkillMetadata, error)
	LoadSkillInstructions(name string) (*Skill, error)
	LoadSkillFile(name, relativePath string) (*SkillFile, error)
	ListSkillFiles(name string) ([]string, error)
	GetSkillBasePath(name string) (string, error)
}

var _ SkillSource = (*Loader)(nil)

// imageSkillSource is a source whose skills already exist inside the sandbox
// image. It is what separates the two sources at execution time: a preloaded
// skill lives on the WeKnora host and has to be uploaded into the sandbox,
// while an installed skill is already there and is executed in place.
type imageSkillSource interface {
	SkillSource

	// RemoteScriptPath returns the absolute in-sandbox path of one script.
	RemoteScriptPath(name, relativePath string) (string, error)
}

var _ imageSkillSource = (*TenantSkillSource)(nil)
