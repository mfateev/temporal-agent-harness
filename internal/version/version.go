// Package version provides build-time version information.
//
// Set at build time via:
//
//	go build -ldflags "-X github.com/mfateev/codex-temporal-go/internal/version.GitCommit=$(git rev-parse --short HEAD)"
package version

// GitCommit is the short git commit hash, set at build time via ldflags.
var GitCommit = "dev"
