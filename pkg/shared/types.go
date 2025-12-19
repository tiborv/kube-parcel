package shared

import "time"

// State represents the server's current state
type State int

const (
	StateIdle         State = iota // Waiting for assets
	StateTransferring              // Receiving/unpacking stream
	StateStarting                  // K3s booting
	StateReady                     // K3s running
)

func (s State) String() string {
	switch s {
	case StateIdle:
		return "IDLE"
	case StateTransferring:
		return "TRANSFERRING"
	case StateStarting:
		return "STARTING"
	case StateReady:
		return "READY"
	default:
		return "UNKNOWN"
	}
}

// StatusResponse is returned by the status endpoint
type StatusResponse struct {
	State            string                 `json:"state"`
	Uptime           int                    `json:"uptime"`
	K3sReady         bool                   `json:"k3s_ready"`
	ChartsCount      int                    `json:"charts_count"`
	ImagesCount      int                    `json:"images_count"`
	Images           []string               `json:"images"`
	StartTime        time.Time              `json:"start_time"`
	ClusterStatus    string                 `json:"cluster_status"` // "Initializing", "Ready", "Error"
	Charts           map[string]ChartStatus `json:"charts"`
	ClusterResources []KubeResource         `json:"cluster_resources"`
}

// ChartStatus represents the state of a Helm chart
type ChartStatus struct {
	Phase   string `json:"phase"`   // Pending, Installing, Deployed, Testing, Succeeded, Failed
	Message string `json:"message"` // Additional details
}

// KubeResource represents a Kubernetes resource managed by a chart
type KubeResource struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Status    string `json:"status"` // e.g., "Running", "Created", "Ready", "Succeeded", "Failed"
	IsTest    bool   `json:"is_test,omitempty"`
	ExitCode  *int   `json:"exit_code,omitempty"` // Pod exit code (nil if not applicable)
}

// LogMessage represents a log entry
type LogMessage struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Source    string    `json:"source"` // "k3s", "helm", "server"
	Message   string    `json:"message"`
}

// Protocol constants
const (
	MagicHeader       = "KUBE-PARCEL-V1"
	ContentTypeParcel = "application/x-parcel-tar"
)
