package plugin

// HostVersion is the WeKnora host version used to validate plugin manifests'
// weknora_version constraints at load/registration time. It is overridable at
// build time via:
//
//	go build -ldflags "-X github.com/Tencent/WeKnora/internal/plugin.HostVersion=x.y.z"
//
// The default matches the repository VERSION file so that even development
// builds enforce version compatibility and reject plugins that declare an
// unsatisfiable weknora_version range with a clear startup error.
var HostVersion = "0.7.2"
