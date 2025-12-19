package config

import (
	"testing"
	"time"
)

func TestPathConstants(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected string
	}{
		{"DefaultKubeconfigPath", DefaultKubeconfigPath, "/tmp/kubeconfig.yaml"},
		{"DefaultImagesDir", DefaultImagesDir, "/tmp/parcel/images"},
		{"DefaultChartsDir", DefaultChartsDir, "/tmp/parcel/charts"},
		{"ContainerdSocket", ContainerdSocket, "/run/k3s/containerd/containerd.sock"},
		{"ContainerdNamespace", ContainerdNamespace, "k8s.io"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.value != tc.expected {
				t.Errorf("%s = %q, expected %q", tc.name, tc.value, tc.expected)
			}
		})
	}
}

func TestNetworkConstants(t *testing.T) {
	if DefaultHTTPPort != 8080 {
		t.Errorf("DefaultHTTPPort = %d, expected 8080", DefaultHTTPPort)
	}
	if DefaultGRPCPort != 9090 {
		t.Errorf("DefaultGRPCPort = %d, expected 9090", DefaultGRPCPort)
	}
}

func TestTimeoutConstants(t *testing.T) {
	tests := []struct {
		name     string
		value    time.Duration
		expected time.Duration
	}{
		{"ImageImportTimeout", ImageImportTimeout, 2 * time.Minute},
		{"K3sReadinessTimeout", K3sReadinessTimeout, 5 * time.Minute},
		{"PodWaitTimeout", PodWaitTimeout, 5 * time.Minute},
		{"ServerReadinessTimeout", ServerReadinessTimeout, 300 * time.Second},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.value != tc.expected {
				t.Errorf("%s = %v, expected %v", tc.name, tc.value, tc.expected)
			}
		})
	}
}

func TestK3sConstants(t *testing.T) {
	if K3sBinary != "/bin/k3s" {
		t.Errorf("K3sBinary = %q, expected \"/bin/k3s\"", K3sBinary)
	}
}
