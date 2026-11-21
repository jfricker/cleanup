package main

import (
	"regexp"
	"runtime/debug"
)

// version is set at release time with -ldflags "-X main.version=v0.0.1".
var version string

var releaseVersion = regexp.MustCompile(`^v\d+\.\d+\.\d+(\+dirty)?$`)

// resolveVersion picks the ldflag value, else a clean tagged module version
// from build info, else "dev". Pseudo-versions and "(devel)" map to "dev".
func resolveVersion(ldflag string, bi *debug.BuildInfo, ok bool) string {
	if ldflag != "" {
		return ldflag
	}
	if ok && bi != nil && releaseVersion.MatchString(bi.Main.Version) {
		return bi.Main.Version
	}
	return "dev"
}

// currentVersion resolves the running binary's version.
func currentVersion() string {
	bi, ok := debug.ReadBuildInfo()
	return resolveVersion(version, bi, ok)
}
