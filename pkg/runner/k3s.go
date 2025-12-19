package runner

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tiborv/kube-parcel/pkg/config"
)

// K3sManager manages the K3s lifecycle
type K3sManager struct {
	cmd            *exec.Cmd
	ready          bool
	kubeconfigPath string
	Airgap         bool // If true (default), K3s won't pull external images
}

// NewK3sManager creates a new K3s manager
func NewK3sManager() *K3sManager {
	return &K3sManager{
		kubeconfigPath: config.DefaultKubeconfigPath,
		Airgap:         true, // Default to airgap mode
	}
}

// Start starts the K3s server process
func (km *K3sManager) Start(ctx context.Context, logWriter io.Writer) error {
	log.Println("� Starting K3s server...")

	// Prepare cgroups for K3s in Cgroupv2 environment
	if err := km.setupCgroups(); err != nil {
		log.Printf("Warning: cgroup setup failed (might be non-cgroupv2): %v", err)
	}

	// Skip airgap for nested K3s
	if km.Airgap && os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
		if err := km.setupAirgapNetwork(); err != nil {
			log.Printf("Warning: airgap network setup failed: %v", err)
		}
	}

	clusterCIDR := "10.42.0.0/16"
	serviceCIDR := "10.43.0.0/16"
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		clusterCIDR = "10.52.0.0/16"
		serviceCIDR = "10.53.0.0/16"
	}

	args := []string{
		"server",
		"--disable=traefik",
		"--disable=servicelb",
		"--disable-cloud-controller",
		"--write-kubeconfig-mode=644",
		"--write-kubeconfig=" + km.kubeconfigPath,
		"--kubelet-arg=--cgroup-driver=cgroupfs",
		"--kubelet-arg=--eviction-hard=",
		"--kubelet-arg=--eviction-soft=",
		"--kubelet-arg=--fail-swap-on=false",
		"--kubelet-arg=--cgroups-per-qos=false",
		"--kubelet-arg=--enforce-node-allocatable=",
		"--cluster-cidr=" + clusterCIDR,
		"--service-cidr=" + serviceCIDR,
	}

	if km.Airgap {
		log.Println("🔒 Airgap mode enabled - blocking external network access")
		args = append(args, "--disable=metrics-server")
	}

	km.cmd = exec.CommandContext(ctx, "/bin/k3s", args...)
	km.cmd.Env = append(os.Environ(), "KUBECONFIG="+km.kubeconfigPath)

	km.cmd.Stdout = logWriter
	km.cmd.Stderr = logWriter

	if err := km.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start k3s: %w", err)
	}

	log.Printf("K3s started with PID %d", km.cmd.Process.Pid)

	if err := km.waitForKubeconfig(); err != nil {
		return err
	}

	os.Setenv("KUBECONFIG", km.kubeconfigPath)
	log.Printf("KUBECONFIG set to %s", km.kubeconfigPath)

	if err := km.waitForReady(); err != nil {
		return err
	}

	km.ready = true
	log.Println("✅ K3s is ready!")
	return nil
}

func (km *K3sManager) waitForKubeconfig() error {
	log.Println("Waiting for kubeconfig generation...")

	timeout := time.After(60 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for kubeconfig at %s", km.kubeconfigPath)
		case <-ticker.C:
			if _, err := os.Stat(km.kubeconfigPath); err == nil {
				log.Println("Kubeconfig generated")
				return nil
			}
		}
	}
}

func (km *K3sManager) waitForReady() error {
	log.Println("Checking K3s API readiness...")

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   5 * time.Second,
	}

	timeout := time.After(300 * time.Second) // 5 minutes
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for k3s API (5 minute limit reached)")
		case <-ticker.C:
			urls := []string{
				"http://127.0.0.1:10248/healthz",
				"https://127.0.0.1:6443/readyz",
			}

			for _, url := range urls {
				resp, err := client.Get(url)
				if err == nil {
					resp.Body.Close()
					if resp.StatusCode == http.StatusOK {
						log.Printf("K3s API is ready (via %s)", url)
						return nil
					}
					if resp.StatusCode == http.StatusUnauthorized {
						log.Printf("K3s API %s is ready (401 = auth required, API is up)", url)
						return nil
					}
					log.Printf("K3s API %s returned status: %d, continuing to wait...", url, resp.StatusCode)
				}
			}
		}
	}
}

// setupCgroups prepares the cgroupv2 hierarchy for nested K3s.
func (km *K3sManager) setupCgroups() error {
	cgroupRoot := "/sys/fs/cgroup"
	if _, err := os.Stat(filepath.Join(cgroupRoot, "cgroup.controllers")); err != nil {
		return nil // Not cgroupv2 or not mounted
	}

	log.Println("Setting up cgroupv2 hierarchy for K3s...")

	initCgroup := filepath.Join(cgroupRoot, "init")
	if err := os.MkdirAll(initCgroup, 0755); err != nil {
		return fmt.Errorf("failed to create init cgroup: %w", err)
	}

	procs, err := os.ReadFile(filepath.Join(cgroupRoot, "cgroup.procs"))
	if err != nil {
		return fmt.Errorf("failed to read root cgroup.procs: %w", err)
	}

	for _, pidStr := range strings.Split(string(procs), "\n") {
		pidStr = strings.TrimSpace(pidStr)
		if pidStr == "" {
			continue
		}
		_ = os.WriteFile(filepath.Join(initCgroup, "cgroup.procs"), []byte(pidStr), 0644)
	}

	essentialControllers := []string{"cpu", "memory", "pids"}
	var enabledControllers []string

	controllers, err := os.ReadFile(filepath.Join(cgroupRoot, "cgroup.controllers"))
	if err != nil {
		return fmt.Errorf("failed to read available controllers: %w", err)
	}

	available := strings.Fields(string(controllers))
	for _, essential := range essentialControllers {
		for _, avail := range available {
			if avail == essential {
				enabledControllers = append(enabledControllers, "+"+essential)
				break
			}
		}
	}

	if len(enabledControllers) > 0 {
		subtree := strings.Join(enabledControllers, " ")
		if err := os.WriteFile(filepath.Join(cgroupRoot, "cgroup.subtree_control"), []byte(subtree), 0644); err != nil {
			return fmt.Errorf("failed to write subtree_control: %w", err)
		}
		log.Printf("Enabled essential cgroup controllers: %v", enabledControllers)
	}

	log.Println("Cgroupv2 hierarchy prepared successfully")
	return nil
}

// setupAirgapNetwork configures iptables to block external network access
// while allowing internal cluster traffic (pod-to-pod, service traffic, etc.)
func (km *K3sManager) setupAirgapNetwork() error {
	log.Println("Setting up airgap network isolation...")

	iptablesRules := [][]string{
		{"-A", "OUTPUT", "-m", "state", "--state", "ESTABLISHED,RELATED", "-j", "ACCEPT"},
		{"-A", "OUTPUT", "-o", "lo", "-j", "ACCEPT"},
		{"-A", "OUTPUT", "-o", "cni+", "-j", "ACCEPT"},
		{"-A", "OUTPUT", "-o", "flannel+", "-j", "ACCEPT"},
		{"-A", "OUTPUT", "-d", "10.0.0.0/8", "-j", "ACCEPT"},
		{"-A", "OUTPUT", "-d", "172.16.0.0/12", "-j", "ACCEPT"},
		{"-A", "OUTPUT", "-d", "192.168.0.0/16", "-j", "ACCEPT"},
		{"-A", "OUTPUT", "-d", "127.0.0.0/8", "-j", "ACCEPT"},
		{"-A", "OUTPUT", "-m", "limit", "--limit", "5/min", "-j", "LOG", "--log-prefix", "AirgapDropped: "},
		{"-A", "OUTPUT", "-j", "DROP"},
	}

	for _, rule := range iptablesRules {
		cmd := exec.Command("iptables", rule...)
		if output, err := cmd.CombinedOutput(); err != nil {
			log.Printf("Warning: iptables rule failed: %v (output: %s)", err, string(output))
		}
	}

	log.Println("🔒 Airgap network isolation configured - external traffic blocked")
	return nil
}

func (km *K3sManager) IsReady() bool {
	return km.ready
}

// Wait waits for the K3s process to exit
func (km *K3sManager) Wait() error {
	if km.cmd == nil || km.cmd.Process == nil {
		return nil
	}
	return km.cmd.Wait()
}

// Stop gracefully stops K3s
func (km *K3sManager) Stop() error {
	if km.cmd == nil || km.cmd.Process == nil {
		return nil
	}

	log.Println("Stopping K3s...")
	return km.cmd.Process.Signal(os.Interrupt)
}
