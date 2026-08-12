package version

import (
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

func TestInfoString(t *testing.T) {
	cases := map[string]struct {
		info Info
		want string
	}{
		"release build": {
			info: Info{
				Version:   "1.4.0",
				Commit:    "c0d3ddc1e5f9a2b3c4d5e6f708192a3b4c5d6e7f",
				TreeState: clean,
				Date:      "2026-05-06T11:01:25Z",
				GoVersion: "go1.26.5",
			},
			want: "kem-agent has version 1.4.0 built with go1.26.5 from c0d3ddc on 2026-05-06T11:01:25Z",
		},
		"dirty tree": {
			info: Info{
				Version:   "1.4.0",
				Commit:    "c0d3ddc1e5f9a2b3c4d5e6f708192a3b4c5d6e7f",
				TreeState: dirty,
				Date:      "2026-05-06T11:01:25Z",
				GoVersion: "go1.26.5",
			},
			want: "kem-agent has version 1.4.0 built with go1.26.5 from c0d3ddc on 2026-05-06T11:01:25Z (dirty)",
		},
		"nothing resolved": {
			info: withDefaults(Info{GoVersion: "go1.26.5"}),
			want: "kem-agent has version dev built with go1.26.5 from unknown on unknown",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tc.info.String(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInfoSemver(t *testing.T) {
	cases := map[string]struct {
		version string
		want    string
	}{
		"a module version drops the v prefix": {version: "v1.2.3", want: "1.2.3"},
		"a stamped version is kept":           {version: "1.4.0", want: "1.4.0"},
		"a prerelease keeps its parts":        {version: "v1.4.0-rc.1+build.7", want: "1.4.0-rc.1+build.7"},
		"a dev build is kept":                 {version: devVersion, want: devVersion},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := (Info{Version: tc.version}).Semver(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFillFromBuildInfo(t *testing.T) {
	cases := map[string]struct {
		info Info
		want Info
	}{
		"an unstamped build takes the VCS state": {
			info: Info{},
			want: Info{
				Version:   "v6.9.0",
				Commit:    "abc123",
				TreeState: dirty,
				Date:      "2026-05-06T11:01:25Z",
			},
		},
		"link-time have precedence": {
			info: Info{
				Version:   "1.4.0",
				Commit:    "def456",
				TreeState: clean,
				Date:      "2026-08-11T09:00:00Z",
			},
			want: Info{
				Version:   "1.4.0",
				Commit:    "def456",
				TreeState: clean,
				Date:      "2026-08-11T09:00:00Z",
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fillFromBuildInfo(&tc.info, newBuildInfo())

			if tc.info != tc.want {
				t.Errorf("got %+v, want %+v", tc.info, tc.want)
			}
		})
	}
}

func TestFillFromBuildInfoWithoutVCS(t *testing.T) {
	var info Info
	fillFromBuildInfo(&info, &debug.BuildInfo{
		Main: debug.Module{
			Version: "(devel)",
		},
	})
	want := Info{Version: "(devel)"}
	if info != want {
		t.Errorf("got %+v, want %+v", info, want)
	}
}

func TestWithDefaults(t *testing.T) {
	cases := map[string]struct {
		info Info
		want Info
	}{
		"empty reads as unknown": {
			info: Info{},
			want: Info{Version: devVersion, Commit: unknown, TreeState: unknown, Date: unknown},
		},
		"a devel module version is a dev build": {
			info: Info{Version: "(devel)"},
			want: Info{Version: devVersion, Commit: unknown, TreeState: unknown, Date: unknown},
		},
		"resolved values are kept": {
			info: Info{Version: "1.4.0", Commit: "abc123", TreeState: clean, Date: "2026-05-06T11:01:25Z"},
			want: Info{Version: "1.4.0", Commit: "abc123", TreeState: clean, Date: "2026-05-06T11:01:25Z"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := withDefaults(tc.info); got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestShortCommit(t *testing.T) {
	cases := map[string]struct {
		sha  string
		want string
	}{
		"a full sha is cut to seven chars": {sha: "c0d3ddc1e5f9a2b3c4d5e6f708192a3b4c5d6e7f", want: "c0d3ddc"},
		"exactly seven chars are kept":     {sha: "c0d3ddc", want: "c0d3ddc"},
		"a shorter value is kept as-is":    {sha: "abc", want: "abc"},
		"unknown is kept":                  {sha: unknown, want: unknown},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := shortCommit(tc.sha); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGetResolvesEveryField(t *testing.T) {
	info := Get()

	if info.Version == "" || info.Commit == "" || info.TreeState == "" || info.Date == "" {
		t.Errorf("got %+v, want every field set", info)
	}
	if info.GoVersion != runtime.Version() {
		t.Errorf("got go version %q, want %q", info.GoVersion, runtime.Version())
	}
}

func TestMetricNamespaceFollowsName(t *testing.T) {
	if want := strings.ReplaceAll(Name, "-", "_"); MetricNamespace != want {
		t.Errorf("got %q, want %q", MetricNamespace, want)
	}
}

func newBuildInfo() *debug.BuildInfo {
	return &debug.BuildInfo{
		Main: debug.Module{Version: "v6.9.0"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abc123"},
			{Key: "vcs.time", Value: "2026-05-06T11:01:25Z"},
			{Key: "vcs.modified", Value: "true"},
		},
	}
}
