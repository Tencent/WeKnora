package codeinterpreter

// DefaultTemplate is the default sandbox template used for the code interpreter.
const DefaultTemplate = "code-interpreter-v1"

// JupyterPort is the internal port on which the Jupyter/Code-Interpreter server
// listens inside the sandbox.
const JupyterPort = 49999

// DefaultTimeout is the default timeout for code execution in seconds.
const DefaultTimeout = 300

// DefaultSandboxTimeout is the default lifetime of a freshly created sandbox in
// seconds (matches e2b defaults).
const DefaultSandboxTimeout = 300

// DefaultRequestTimeout is the default HTTP request timeout in seconds.
const DefaultRequestTimeout = 30

// DefaultDomain is the default e2b.dev API domain.
const DefaultDomain = "e2b.app"
