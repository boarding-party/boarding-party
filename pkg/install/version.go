package install

import (
	"time"

	appsv1 "k8s.io/api/apps/v1"
)

// InstalledVersion reads what a director Deployment runs: its image tag and
// when the CLI that deployed it was built (zero when not recorded).
func InstalledVersion(dep *appsv1.Deployment) (tag string, cliBuilt time.Time) {
	cliBuilt, _ = time.Parse(time.RFC3339, dep.Annotations[CLIBuiltAnnotation])
	return ImageTag(dep), cliBuilt
}
