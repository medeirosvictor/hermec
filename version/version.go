// Package version holds the build version shared by the Hermec binaries.
package version

// Version is the Hermec release version. The release pipeline stamps it at
// build time via -ldflags "-X github.com/medeirosvictor/hermec/version.Version=...".
var Version = "dev"
