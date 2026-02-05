// Package version provides version information for the operator.
// Variables are set via ldflags at build time.
package version

// These variables are set via ldflags at build time.
// Example: go build -ldflags "-X 'github.com/truenas/truenas-csi/operator/internal/version.Version=1.0.0'"
var (
	// Version is the semantic version of the operator
	Version = "0.1.0"

	// GitVersion is the output of `git describe --dirty --tags --always`
	GitVersion = "unknown"

	// GitCommit is the full git commit hash
	GitCommit = "unknown"
)

// Identity constants for the operator and CSI driver
const (
	// OperatorName is the name of the operator
	OperatorName = "truenas-csi-operator"

	// DriverName is the name of the CSI driver
	DriverName = "truenas-csi"

	// CSIDriverName is the full CSI driver identifier registered with Kubernetes
	CSIDriverName = "csi.truenas.io"

	// Registry is the default container registry for images
	Registry = "quay.io/truenas_solutions"
)

// GetVersion returns the current version string
func GetVersion() string {
	return Version
}

// GetFullVersion returns version with git info
func GetFullVersion() string {
	if GitVersion != "unknown" {
		return GitVersion
	}
	return Version
}
