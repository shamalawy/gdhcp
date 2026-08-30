// Package version holds build-time identity, stamped via linker flags:
//
//	go build -ldflags "-X github.com/shamalawy/gdhcp/server/internal/version.Version=1.2.3 \
//	  -X github.com/shamalawy/gdhcp/server/internal/version.Commit=$(git rev-parse --short HEAD) \
//	  -X github.com/shamalawy/gdhcp/server/internal/version.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
package version

// Version, Commit, and BuildDate are set at build time via -ldflags -X.
// They default to "dev"/"none"/"unknown" for `go run`/unstamped builds.
var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

// Info bundles the three build-time values for structured logging and the
// `gdhcp version` / GET /api/v1/health responses.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
}

// Get returns the current build Info.
func Get() Info {
	return Info{Version: Version, Commit: Commit, BuildDate: BuildDate}
}

// String renders a short human-readable identifier, e.g. "1.2.3 (abcdef1, 2026-08-29T10:00:00Z)".
func (i Info) String() string {
	return i.Version + " (" + i.Commit + ", " + i.BuildDate + ")"
}
