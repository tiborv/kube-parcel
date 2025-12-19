package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tiborv/kube-parcel/pkg/config"
	"github.com/tiborv/kube-parcel/pkg/shared"
)

// HelmManager handles Helm operations
type HelmManager struct {
	chartsDir   string
	logger      io.Writer
	chartStatus map[string]shared.ChartStatus
	mu          sync.RWMutex
}

// NewHelmManager creates a new Helm manager
func NewHelmManager(logger io.Writer) *HelmManager {
	return &HelmManager{
		chartsDir:   config.DefaultChartsDir,
		logger:      logger,
		chartStatus: make(map[string]shared.ChartStatus),
	}
}

// InstallCharts installs all charts in the charts directory
func (hm *HelmManager) InstallCharts() error {
	if err := hm.ensureHelmBinary(); err != nil {
		return fmt.Errorf("failed to ensure helm binary: %w", err)
	}

	charts, err := hm.discoverCharts()
	if err != nil {
		return err
	}

	if len(charts) == 0 {
		log.Println("No charts found to install")
		return nil
	}

	// Wait for default namespace to be fully bootstrapped
	if err := hm.waitForDefaultServiceAccount(); err != nil {
		log.Printf("Warning: could not wait for default serviceaccount: %v", err)
		// Continue anyway, some charts may not need it
	}

	log.Printf("Found %d chart(s) to install", len(charts))

	var testFailures []string
	for _, chart := range charts {
		if err := hm.installChart(chart); err != nil {
			log.Printf("Warning: failed to install chart %s: %v", chart, err)
			testFailures = append(testFailures, chart)
			continue
		}
		if err := hm.runTests(chart); err != nil {
			log.Printf("Warning: failed to run tests for chart %s: %v", chart, err)
			testFailures = append(testFailures, chart)
		}
	}

	if len(testFailures) > 0 {
		return fmt.Errorf("tests failed for %d chart(s): %v", len(testFailures), testFailures)
	}

	return nil
}

// waitForDefaultServiceAccount waits for the default namespace to have a default serviceaccount
// This is needed because K8s namespaces take a moment to fully bootstrap
func (hm *HelmManager) waitForDefaultServiceAccount() error {
	log.Println("Waiting for default serviceaccount to be created...")
	timeout := time.After(60 * time.Second)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for default serviceaccount")
		case <-ticker.C:
			cmd := exec.Command("kubectl", "get", "serviceaccount", "default", "-n", "default")
			cmd.Env = append(os.Environ(), "KUBECONFIG="+config.DefaultKubeconfigPath)
			if err := cmd.Run(); err == nil {
				log.Println("✅ Default serviceaccount is ready")
				return nil
			}
		}
	}
}

// discoverCharts finds all Helm charts in the charts directory
func (hm *HelmManager) discoverCharts() ([]string, error) {
	var charts []string

	if _, err := os.Stat(hm.chartsDir); os.IsNotExist(err) {
		return charts, nil
	}

	entries, err := os.ReadDir(hm.chartsDir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		chartPath := filepath.Join(hm.chartsDir, entry.Name())
		chartYaml := filepath.Join(chartPath, "Chart.yaml")

		if _, err := os.Stat(chartYaml); err == nil {
			charts = append(charts, chartPath)
		}
	}

	return charts, nil
}

// installChart installs a single chart
func (hm *HelmManager) installChart(chartPath string) error {
	chartName := filepath.Base(chartPath)
	releaseName := strings.ToLower(chartName)

	log.Printf("📦 Installing chart: %s (release: %s)", chartName, releaseName)
	fmt.Fprintf(hm.logger, "Installing chart: %s\n", chartName)
	hm.updateStatus(chartName, "Installing", "Helm install started")

	cmd := exec.Command("helm", "install", releaseName, chartPath, "--wait", "--timeout=15m")
	cmd.Env = append(os.Environ(), "KUBECONFIG="+config.DefaultKubeconfigPath)

	cmd.Stdout = hm.logger
	cmd.Stderr = hm.logger

	if err := cmd.Run(); err != nil {
		errMsg := fmt.Sprintf("Install failed: %v", err)
		log.Printf("❌ Chart %s install failed: %v", chartName, err)
		fmt.Fprintf(hm.logger, "❌ Install failed: %s\n", errMsg)
		hm.updateStatus(chartName, "Failed", errMsg)
		return fmt.Errorf("helm install failed: %w", err)
	}

	log.Printf("✅ Chart %s installed successfully", chartName)
	fmt.Fprintf(hm.logger, "✅ Chart %s installed successfully\n", chartName)
	hm.updateStatus(chartName, "Deployed", "Helm install succeeded")
	return nil
}

// runTests runs helm test for a release
func (hm *HelmManager) runTests(chartPath string) error {
	chartName := filepath.Base(chartPath)
	releaseName := strings.ToLower(chartName)

	log.Printf("🧪 Running tests for release: %s", releaseName)
	fmt.Fprintf(hm.logger, "Running tests for: %s\n", releaseName)
	hm.updateStatus(chartName, "Testing", "Running integration tests")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hm.streamTestLogs(ctx, releaseName)

	cmd := exec.Command("helm", "test", releaseName, "--logs", "--timeout=15m")
	cmd.Env = append(os.Environ(), "KUBECONFIG="+config.DefaultKubeconfigPath)

	cmd.Stdout = hm.logger
	cmd.Stderr = hm.logger

	if err := cmd.Run(); err != nil {
		errMsg := fmt.Sprintf("Tests failed: %v", err)
		log.Printf("❌ Tests failed for %s: %v", releaseName, err)
		fmt.Fprintf(hm.logger, "❌ Tests failed: %s\n", errMsg)
		hm.updateStatus(chartName, "Failed", errMsg)
		return fmt.Errorf("helm test failed: %w", err)
	}

	log.Printf("✅ Tests passed for %s", releaseName)
	fmt.Fprintf(hm.logger, "✅ Tests passed for %s\n", releaseName)
	hm.updateStatus(chartName, "Succeeded", "All tests passed")
	return nil
}

// streamTestLogs streams logs from the test pod(s)
func (hm *HelmManager) streamTestLogs(ctx context.Context, releaseName string) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var podName string

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			labelSelector := fmt.Sprintf("helm.sh/hook=test,app.kubernetes.io/instance=%s", releaseName)
			cmd := exec.Command("kubectl", "get", "pods", "-l", labelSelector, "-o", "jsonpath={.items[0].metadata.name}")
			cmd.Env = append(os.Environ(), "KUBECONFIG="+config.DefaultKubeconfigPath)
			out, err := cmd.Output()
			if err == nil && len(out) > 0 {
				podName = string(out)
				break
			}
		}
		if podName != "" {
			break
		}
	}

	log.Printf("📡 Found test pod %s, streaming logs...", podName)
	fmt.Fprintf(hm.logger, "📡 Found test pod %s, streaming logs...\n", podName)

	cmd := exec.CommandContext(ctx, "kubectl", "logs", "-f", podName)
	cmd.Env = append(os.Environ(), "KUBECONFIG="+config.DefaultKubeconfigPath)
	cmd.Stdout = hm.logger
	cmd.Stderr = hm.logger

	_ = cmd.Run()
}

// ensureHelmBinary checks if helm is installed, and if not, downloads it
func (hm *HelmManager) ensureHelmBinary() error {
	for _, helmPath := range []string{"/bin/helm", "/usr/local/bin/helm", "/usr/bin/helm"} {
		if _, err := os.Stat(helmPath); err == nil {
			log.Printf("✅ Found Helm at %s", helmPath)
			os.Setenv("PATH", filepath.Dir(helmPath)+":"+os.Getenv("PATH"))
			return nil
		}
	}

	if _, err := exec.LookPath("helm"); err == nil {
		log.Println("✅ Found Helm in PATH")
		return nil
	}

	log.Println("🔧 Helm binary not found in any location, downloading...")
	fmt.Fprintf(hm.logger, "Helm not found, downloading...\n")

	tmpDir, err := os.MkdirTemp("", "helm-install")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	helmUrl := config.HelmDownloadURL
	tarPath := filepath.Join(tmpDir, "helm.tar.gz")

	if err := downloadFile(helmUrl, tarPath); err != nil {
		return fmt.Errorf("failed to download helm: %w", err)
	}

	cmd := exec.Command("tar", "-xzf", tarPath, "-C", tmpDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to extract helm: %v (output: %s)", err, out)
	}

	srcPath := filepath.Join(tmpDir, "linux-amd64", "helm")
	destPath := "/bin/helm"

	input, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("failed to read extracted helm: %w", err)
	}

	if err := os.WriteFile(destPath, input, 0755); err != nil {
		destPath = "/usr/local/bin/helm"
		if err := os.WriteFile(destPath, input, 0755); err != nil {
			return fmt.Errorf("failed to install helm binary to /bin or /usr/local/bin: %w", err)
		}
	}

	log.Printf("✅ Installed Helm to %s", destPath)
	return nil
}

func downloadFile(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func (hm *HelmManager) updateStatus(chart, phase, message string) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	hm.chartStatus[chart] = shared.ChartStatus{
		Phase:   phase,
		Message: message,
	}
}

func (hm *HelmManager) GetChartsStatus() map[string]shared.ChartStatus {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	// Copy map to avoid races
	status := make(map[string]shared.ChartStatus)
	for k, v := range hm.chartStatus {
		status[k] = v
	}
	return status
}

// FetchAllClusterResources returns all resources in the cluster across all namespaces
func (hm *HelmManager) FetchAllClusterResources() []shared.KubeResource {
	var resources []shared.KubeResource

	cmd := exec.Command("kubectl", "get", "pods,svc,deploy,sts,ds,job,ing,pvc,configmap,secret", "-A", "-o", "json")
	cmd.Env = append(os.Environ(), "KUBECONFIG="+config.DefaultKubeconfigPath)

	out, err := cmd.Output()
	if err != nil {
		log.Printf("Warning: failed to fetch cluster resources: %v", err)
		return nil
	}

	var data struct {
		Items []struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name        string            `json:"name"`
				Namespace   string            `json:"namespace"`
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
			Status struct {
				Phase             string `json:"phase"`
				ContainerStatuses []struct {
					State struct {
						Terminated *struct {
							ExitCode int `json:"exitCode"`
						} `json:"terminated,omitempty"`
					} `json:"state"`
				} `json:"containerStatuses,omitempty"`
			} `json:"status"`
		} `json:"items"`
	}

	if err := json.Unmarshal(out, &data); err != nil {
		log.Printf("Warning: failed to unmarshal kubectl output: %v", err)
		return nil
	}

	for _, item := range data.Items {
		status := item.Status.Phase
		if status == "" {
			status = "Active"
		}

		// Extract exit code from terminated containers (for Pods)
		var exitCode *int
		if item.Kind == "Pod" && len(item.Status.ContainerStatuses) > 0 {
			for _, cs := range item.Status.ContainerStatuses {
				if cs.State.Terminated != nil {
					code := cs.State.Terminated.ExitCode
					exitCode = &code
					break
				}
			}
		}

		resources = append(resources, shared.KubeResource{
			Kind:      item.Kind,
			Name:      item.Metadata.Name,
			Namespace: item.Metadata.Namespace,
			Status:    status,
			ExitCode:  exitCode,
		})
	}

	return resources
}
