// Package version holds build information injected at link time.
package version

// These values are overridden at build time via -ldflags "-X ...".
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)
