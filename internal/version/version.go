package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
)

const (
	// Name identifies the agent to the backends it delivers to
	// and in its own command output.
	Name = "kem-agent"

	// MetricNamespace is the reserved Prometheus namespace the
	// agent registers its own instruments under.
	MetricNamespace = "kem_agent"
)

var (
	version   string
	commit    string
	treeState string
	date      string
)

var getOnce = sync.OnceValue(resolve)

const (
	unknown    = "unknown"
	devVersion = "dev"
	dirty      = "dirty"
	clean      = "clean"
)

// Info is the agent's build information. Every field is always set,
// an unresolved one default to "unknown".
type Info struct {
	Version   string
	Commit    string
	TreeState string
	Date      string
	GoVersion string
}

// Get returns the agent's build information, resolved on first use.
func Get() Info {
	return getOnce()
}

// String returns the one-line build summary, naming the toolchain the
// agent was built with and the commit it was built from.
func (i Info) String() string {
	line := fmt.Sprintf(
		"%s has version %s built with %s from %s on %s",
		Name,
		i.Version,
		i.GoVersion,
		shortCommit(i.Commit),
		i.Date,
	)
	if i.TreeState == dirty {
		return line + " (dirty)"
	}
	return line
}

// Semver returns the version in its SemVer form, without the leading 'v'
// prefix the Go module convention adds, so a build resolving its version
// from the module and one stamped at link time are similar.
func (i Info) Semver() string {
	return strings.TrimPrefix(i.Version, "v")
}

func resolve() Info {
	info := Info{
		Version:   version,
		Commit:    commit,
		TreeState: treeState,
		Date:      date,
		GoVersion: runtime.Version(),
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		fillFromBuildInfo(&info, bi)
	}
	return withDefaults(info)
}

func fillFromBuildInfo(info *Info, bi *debug.BuildInfo) {
	if info.Version == "" {
		info.Version = bi.Main.Version
	}
	var (
		revision string
		time     string
		modified string
	)
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.time":
			time = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	if info.Commit == "" {
		info.Commit = revision
	}
	if info.Date == "" {
		info.Date = time
	}
	if info.TreeState == "" && modified != "" {
		info.TreeState = treeStateOf(modified)
	}
}

func withDefaults(info Info) Info {
	// Reported by the toolchain for a main module built outside a release.
	const devel = "(devel)"

	if info.Version == "" || info.Version == devel {
		info.Version = devVersion
	}
	if info.Commit == "" {
		info.Commit = unknown
	}
	if info.TreeState == "" {
		info.TreeState = unknown
	}
	if info.Date == "" {
		info.Date = unknown
	}
	return info
}

func treeStateOf(modified string) string {
	if modified == "true" {
		return dirty
	}
	return clean
}

func shortCommit(sha string) string {
	const n = 7

	if len(sha) <= n {
		return sha
	}
	return sha[:n]
}
