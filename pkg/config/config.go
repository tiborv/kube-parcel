// Package config provides centralized configuration constants for kube-parcel.
package config

import "time"

// Version information
const (
	// Version is the current kube-parcel version (semver)
	Version = "0.0.1"

	// MinorVersion is used for runner image tagging (allows patch upgrades without changing runner)
	MinorVersion = "0.0"

	// Helm configuration
	HelmVersion     = "3.13.3"
	HelmDownloadURL = "https://get.helm.sh/helm-v" + HelmVersion + "-linux-amd64.tar.gz"
)

// Path configuration
const (
	// DefaultKubeconfigPath is the path where K3s writes its kubeconfig
	DefaultKubeconfigPath = "/tmp/kubeconfig.yaml"

	// DefaultImagesDir is where extracted image tarballs are stored
	DefaultImagesDir = "/tmp/parcel/images"

	// DefaultChartsDir is where extracted Helm charts are stored
	DefaultChartsDir = "/tmp/parcel/charts"

	// ContainerdSocket is the K3s containerd socket path
	ContainerdSocket = "/run/k3s/containerd/containerd.sock"

	// ContainerdNamespace is the Kubernetes containerd namespace
	ContainerdNamespace = "k8s.io"
)

// Network configuration
const (
	// DefaultHTTPPort is the default HTTP server port
	DefaultHTTPPort = 8080

	// DefaultGRPCPort is the default gRPC server port
	DefaultGRPCPort = 9090
)

// Timeout configuration
const (
	// ImageImportTimeout is the max time to import a single image
	ImageImportTimeout = 2 * time.Minute

	// K3sReadinessTimeout is the max time to wait for K3s API to be ready
	K3sReadinessTimeout = 5 * time.Minute

	// PodWaitTimeout is the max time to wait for a pod to be ready
	PodWaitTimeout = 5 * time.Minute

	// ServerReadinessTimeout is the max time to wait for server HTTP readiness
	ServerReadinessTimeout = 300 * time.Second
)

// K3s configuration
const (
	// K3sBinary is the path to the K3s binary
	K3sBinary = "/bin/k3s"
)
