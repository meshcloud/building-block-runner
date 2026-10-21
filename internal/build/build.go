package build

import (
	"regexp"
	"runtime/debug"
)

var (
	Version  = "dev"
	Revision string
)

func init() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		Version = info.Main.Version
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			Revision = setting.Value
		}
	}
}

var releaseVersion = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// ImageTag is the tag CI pushes the images of this build with, or empty when it is unknown.
func ImageTag() string {
	if releaseVersion.MatchString(Version) {
		return Version
	}
	return Revision
}
