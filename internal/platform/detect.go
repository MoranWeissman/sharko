package platform

import "os"

// Mode represents the runtime environment.
type Mode string

const (
	ModeLocal      Mode = "local"
	ModeKubernetes Mode = "kubernetes"
)

// Detect determines whether the application is running inside a Kubernetes
// cluster or locally. It checks for the KUBERNETES_SERVICE_HOST environment
// variable which is automatically set by Kubernetes in every pod.
func Detect() Mode {
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return ModeKubernetes
	}
	return ModeLocal
}
