package codeinterpreter

import (
	"encoding/json"
	"fmt"
)

// RunCodeLanguage is a supported language identifier for code execution.
//
// Known values: "python", "javascript", "typescript", "r", "java", "bash".
// Custom strings are allowed for user-installed kernels.
type RunCodeLanguage string

const (
	// LanguagePython is the language identifier for Python.
	LanguagePython RunCodeLanguage = "python"
	// LanguageJavaScript is the language identifier for JavaScript.
	LanguageJavaScript RunCodeLanguage = "javascript"
	// LanguageTypeScript is the language identifier for TypeScript.
	LanguageTypeScript RunCodeLanguage = "typescript"
	// LanguageR is the language identifier for R.
	LanguageR RunCodeLanguage = "r"
	// LanguageJava is the language identifier for Java.
	LanguageJava RunCodeLanguage = "java"
	// LanguageBash is the language identifier for Bash.
	LanguageBash RunCodeLanguage = "bash"
)

// MIMEType is a string alias for convenience.
type MIMEType = string

// OutputMessage represents an output message from the sandbox code execution.
type OutputMessage struct {
	// Line is the raw output line.
	Line string
	// Timestamp is the unix epoch in nanoseconds.
	Timestamp int64
	// Error indicates whether this output originates from stderr.
	Error bool
}

func (o OutputMessage) String() string { return o.Line }

// ExecutionError represents an error that occurred during the execution of a cell.
type ExecutionError struct {
	// Name of the error (e.g. "NameError").
	Name string `json:"name"`
	// Value/message of the error.
	Value string `json:"value"`
	// Traceback is the raw traceback text.
	Traceback string `json:"traceback"`
}

func (e *ExecutionError) Error() string {
	return fmt.Sprintf("%s: %s\n%s", e.Name, e.Value, e.Traceback)
}

// ToJSON returns the JSON representation of this error.
func (e *ExecutionError) ToJSON() string {
	b, _ := json.Marshal(e)
	return string(b)
}

// Logs holds data printed to stdout and stderr during execution.
type Logs struct {
	Stdout []string `json:"stdout"`
	Stderr []string `json:"stderr"`
}

// Result represents the data to be displayed as a result of executing a cell in
// a Jupyter notebook.  A Result may carry several representations of the same
// underlying data (text, HTML, PNG, SVG, …).
type Result struct {
	Text       string         `json:"text,omitempty"`
	HTML       string         `json:"html,omitempty"`
	Markdown   string         `json:"markdown,omitempty"`
	SVG        string         `json:"svg,omitempty"`
	PNG        string         `json:"png,omitempty"`
	JPEG       string         `json:"jpeg,omitempty"`
	PDF        string         `json:"pdf,omitempty"`
	LaTeX      string         `json:"latex,omitempty"`
	JSON       map[string]any `json:"json,omitempty"`
	JavaScript string         `json:"javascript,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
	// Chart is the structured chart data extracted by the server, if any.
	Chart Chart `json:"-"`
	// IsMainResult indicates whether this is the primary result of the cell
	// (as opposed to an intermediate display call).
	IsMainResult bool `json:"is_main_result,omitempty"`
	// Extra holds any additional representations not covered by the
	// standard fields above.
	Extra map[string]any `json:"extra,omitempty"`
	// Raw holds the full raw JSON payload as returned by the server.
	Raw map[string]any `json:"-"`
}

// Formats returns the list of MIME-like format names available on this result.
func (r *Result) Formats() []string {
	formats := []string{}
	if r.Text != "" {
		formats = append(formats, "text")
	}
	if r.HTML != "" {
		formats = append(formats, "html")
	}
	if r.Markdown != "" {
		formats = append(formats, "markdown")
	}
	if r.SVG != "" {
		formats = append(formats, "svg")
	}
	if r.PNG != "" {
		formats = append(formats, "png")
	}
	if r.JPEG != "" {
		formats = append(formats, "jpeg")
	}
	if r.PDF != "" {
		formats = append(formats, "pdf")
	}
	if r.LaTeX != "" {
		formats = append(formats, "latex")
	}
	if r.JSON != nil {
		formats = append(formats, "json")
	}
	if r.JavaScript != "" {
		formats = append(formats, "javascript")
	}
	if r.Data != nil {
		formats = append(formats, "data")
	}
	if r.Chart != nil {
		formats = append(formats, "chart")
	}
	for k := range r.Extra {
		formats = append(formats, k)
	}
	return formats
}

// String returns a short description of the result. If a textual representation
// exists it is returned as-is, otherwise the list of available formats is
// returned.
func (r *Result) String() string {
	if r.Text != "" {
		return fmt.Sprintf("Result(%s)", r.Text)
	}
	return fmt.Sprintf("Result(Formats: %v)", r.Formats())
}

// newResultFromRaw builds a Result from the raw JSON map coming from the server.
// This mirrors the behaviour of the Python/JS SDKs.
func newResultFromRaw(raw map[string]any) *Result {
	r := &Result{Raw: raw}

	// Known keys extraction
	knownKeys := map[string]struct{}{
		"type": {}, "is_main_result": {},
		"text": {}, "html": {}, "markdown": {}, "svg": {},
		"png": {}, "jpeg": {}, "pdf": {}, "latex": {},
		"json": {}, "javascript": {}, "data": {}, "chart": {},
		"extra": {},
	}

	r.Text = getString(raw, "text")
	r.HTML = getString(raw, "html")
	r.Markdown = getString(raw, "markdown")
	r.SVG = getString(raw, "svg")
	r.PNG = getString(raw, "png")
	r.JPEG = getString(raw, "jpeg")
	r.PDF = getString(raw, "pdf")
	r.LaTeX = getString(raw, "latex")
	r.JavaScript = getString(raw, "javascript")

	if v, ok := raw["json"].(map[string]any); ok {
		r.JSON = v
	}
	if v, ok := raw["data"].(map[string]any); ok {
		r.Data = v
	}
	if v, ok := raw["is_main_result"].(bool); ok {
		r.IsMainResult = v
	}
	if c, ok := raw["chart"].(map[string]any); ok {
		r.Chart = deserializeChart(c)
	}
	if extra, ok := raw["extra"].(map[string]any); ok {
		r.Extra = extra
	}

	// Collect unknown keys into Extra (keeps parity with JS SDK).
	for k, v := range raw {
		if _, known := knownKeys[k]; known {
			continue
		}
		if r.Extra == nil {
			r.Extra = make(map[string]any)
		}
		r.Extra[k] = v
	}

	return r
}

// Execution represents the result of a cell execution.
type Execution struct {
	// Results collects the cell's main result and any intermediate display
	// calls (e.g. matplotlib plots).
	Results []*Result `json:"results"`
	// Logs holds stdout/stderr lines printed during execution.
	Logs Logs `json:"logs"`
	// Error is set if the execution failed; nil otherwise.
	Error *ExecutionError `json:"error,omitempty"`
	// ExecutionCount is the execution count (cell index) reported by the kernel.
	ExecutionCount int `json:"execution_count,omitempty"`
}

// NewExecution creates an empty Execution.
func NewExecution() *Execution {
	return &Execution{
		Results: []*Result{},
		Logs:    Logs{Stdout: []string{}, Stderr: []string{}},
	}
}

// Text returns the text representation of the main result, if any.
func (e *Execution) Text() string {
	for _, r := range e.Results {
		if r.IsMainResult {
			return r.Text
		}
	}
	return ""
}

// ToJSON serializes the execution to JSON.
func (e *Execution) ToJSON() (string, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Context represents a code execution context (a persistent kernel).
type Context struct {
	// ID of the context.
	ID string `json:"id"`
	// Language of the context.
	Language string `json:"language"`
	// Cwd is the working directory inside the sandbox.
	Cwd string `json:"cwd"`
}

// contextFromJSON decodes a context from a raw JSON map.
func contextFromJSON(data map[string]any) *Context {
	return &Context{
		ID:       getString(data, "id"),
		Language: getString(data, "language"),
		Cwd:      getString(data, "cwd"),
	}
}

// OnStdoutFunc is a callback invoked for every stdout chunk produced by the
// running code.
type OnStdoutFunc func(msg OutputMessage)

// OnStderrFunc is a callback invoked for every stderr chunk produced by the
// running code.
type OnStderrFunc func(msg OutputMessage)

// OnResultFunc is a callback invoked for every Result emitted by the running
// code (display calls as well as the final main result).
type OnResultFunc func(result *Result)

// OnErrorFunc is a callback invoked when the running code raises an error.
type OnErrorFunc func(err *ExecutionError)
