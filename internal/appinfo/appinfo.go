package appinfo

// Name is the user-facing application name.
const Name = "XingheBot"

// Version is the user-facing semantic version.
//
// Keep this as a var so it can be overridden at build time via:
//
//	-ldflags "-X test_skill_agent/internal/appinfo.Version=0.0.2"
var Version = "0.0.1"

func Display() string {
	v := Version
	if v == "" {
		v = "dev"
	}
	return Name + " v" + v
}
