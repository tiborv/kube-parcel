package shared

import "testing"

func TestState_String(t *testing.T) {
	tests := []struct {
		state    State
		expected string
	}{
		{StateIdle, "IDLE"},
		{StateTransferring, "TRANSFERRING"},
		{StateStarting, "STARTING"},
		{StateReady, "READY"},
		{State(999), "UNKNOWN"},
	}

	for _, tc := range tests {
		result := tc.state.String()
		if result != tc.expected {
			t.Errorf("State(%d).String() = %q, expected %q", tc.state, result, tc.expected)
		}
	}
}

func TestState_Constants(t *testing.T) {
	// Verify the order of state constants (iota-based)
	if StateIdle != 0 {
		t.Errorf("StateIdle should be 0, got %d", StateIdle)
	}
	if StateTransferring != 1 {
		t.Errorf("StateTransferring should be 1, got %d", StateTransferring)
	}
	if StateStarting != 2 {
		t.Errorf("StateStarting should be 2, got %d", StateStarting)
	}
	if StateReady != 3 {
		t.Errorf("StateReady should be 3, got %d", StateReady)
	}
}

func TestChartStatus(t *testing.T) {
	status := ChartStatus{
		Phase:   "Deployed",
		Message: "Helm install succeeded",
	}

	if status.Phase != "Deployed" {
		t.Errorf("expected Phase 'Deployed', got %q", status.Phase)
	}
	if status.Message != "Helm install succeeded" {
		t.Errorf("expected Message 'Helm install succeeded', got %q", status.Message)
	}
}

func TestKubeResource(t *testing.T) {
	exitCode := 0
	resource := KubeResource{
		Kind:      "Pod",
		Name:      "test-pod",
		Namespace: "default",
		Status:    "Running",
		IsTest:    true,
		ExitCode:  &exitCode,
	}

	if resource.Kind != "Pod" {
		t.Errorf("expected Kind 'Pod', got %q", resource.Kind)
	}
	if resource.Name != "test-pod" {
		t.Errorf("expected Name 'test-pod', got %q", resource.Name)
	}
	if resource.Namespace != "default" {
		t.Errorf("expected Namespace 'default', got %q", resource.Namespace)
	}
	if resource.Status != "Running" {
		t.Errorf("expected Status 'Running', got %q", resource.Status)
	}
	if !resource.IsTest {
		t.Error("expected IsTest to be true")
	}
	if resource.ExitCode == nil || *resource.ExitCode != 0 {
		t.Error("expected ExitCode to be 0")
	}
}

func TestKubeResource_NilExitCode(t *testing.T) {
	resource := KubeResource{
		Kind:      "Service",
		Name:      "my-service",
		Namespace: "default",
		Status:    "Active",
	}

	if resource.ExitCode != nil {
		t.Error("expected ExitCode to be nil for non-pod resources")
	}
}

func TestProtocolConstants(t *testing.T) {
	if MagicHeader != "KUBE-PARCEL-V1" {
		t.Errorf("MagicHeader = %q, expected 'KUBE-PARCEL-V1'", MagicHeader)
	}
	if ContentTypeParcel != "application/x-parcel-tar" {
		t.Errorf("ContentTypeParcel = %q, expected 'application/x-parcel-tar'", ContentTypeParcel)
	}
}
