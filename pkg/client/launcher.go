package client

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"

	parcelconfig "github.com/tiborv/kube-parcel/pkg/config"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

// generateUniqueName creates a unique name for containers/pods to enable parallel execution
func generateUniqueName() string {
	b := make([]byte, 4)
	rand.Read(b)
	return fmt.Sprintf("kube-parcel-%s", hex.EncodeToString(b))
}

// ServerHandle represents a running server instance
type ServerHandle struct {
	mode        string
	url         string
	cleanup     func() error
	dockerCli   *client.Client
	containerID string
}

// URL returns the server URL
func (h *ServerHandle) URL() string {
	return h.url
}

// Cleanup stops the server
func (h *ServerHandle) Cleanup() error {
	if h.cleanup != nil {
		return h.cleanup()
	}
	return nil
}

// LaunchLocal starts the server using Docker
func LaunchLocal(ctx context.Context, image string, env map[string]string) (*ServerHandle, error) {
	log.Println("🐳 Launching server locally with Docker...")

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	// Note: Add image pull logic if needed

	var envList []string
	for k, v := range env {
		envList = append(envList, fmt.Sprintf("%s=%s", k, v))
	}

	containerConfig := &container.Config{
		Image:      image,
		Entrypoint: []string{"/app/runner"},
		Cmd:        []string{},
		Env:        envList,
		ExposedPorts: nat.PortSet{
			"8080/tcp": struct{}{},
			"9090/tcp": struct{}{},
		},
	}

	hostConfig := &container.HostConfig{
		Privileged:   true,
		CgroupnsMode: "host",
		Tmpfs: map[string]string{
			"/run":     "",
			"/var/run": "",
		},
		// No cgroup mount - K3s will handle internally
		Binds: []string{},
		PortBindings: nat.PortMap{
			"8080/tcp": []nat.PortBinding{
				{HostIP: "", HostPort: "0"}, // Dynamic port for parallel execution
			},
			"9090/tcp": []nat.PortBinding{
				{HostIP: "", HostPort: "0"}, // Dynamic port for parallel execution
			},
		},
	}

	containerName := generateUniqueName()
	log.Printf("Creating container: %s", containerName)

	resp, err := cli.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, containerName)
	if err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	inspect, err := cli.ContainerInspect(ctx, resp.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}

	ports := inspect.NetworkSettings.Ports["8080/tcp"]
	if len(ports) == 0 {
		return nil, fmt.Errorf("no port binding found for 8080/tcp")
	}
	hostPort := ports[0].HostPort
	serverURL := fmt.Sprintf("http://localhost:%s", hostPort)

	log.Printf("✅ Container started: %s (port %s)", containerName, hostPort)
	log.Println("Waiting for server to be ready...")

	if err := waitForServer(ctx, serverURL); err != nil {
		return nil, fmt.Errorf("server failed to become ready: %w", err)
	}

	handle := &ServerHandle{
		mode:        "local",
		url:         serverURL,
		dockerCli:   cli,
		containerID: resp.ID,
		cleanup: func() error {
			log.Println("Stopping container...")
			timeout := 10
			return cli.ContainerStop(ctx, resp.ID, container.StopOptions{Timeout: &timeout})
		},
	}

	return handle, nil
}

// PodSettings defines customizations for the master pod
type PodSettings struct {
	Namespace   string
	Image       string
	CPU         string
	Memory      string
	Labels      map[string]string
	Annotations map[string]string
	Command     []string
	Args        []string
	Env         []corev1.EnvVar
	HostPID     bool // Use host PID namespace for better nested container support
}

// LaunchRemote starts the server using Kubernetes
func LaunchRemote(ctx context.Context, settings PodSettings) (*ServerHandle, error) {
	log.Printf("☸️  Launching server in Kubernetes (ns: %s, image: %s)...", settings.Namespace, settings.Image)

	if len(settings.Command) == 0 {
		settings.Command = []string{"/app/runner"}
	}

	var config *rest.Config
	var err error
	config, err = rest.InClusterConfig()
	if err == nil {
		log.Println("✅ Using in-cluster configuration")
	}
	if err != nil {
		log.Println("Not running in-cluster, falling back to kubeconfig...")
		kubeconfig := filepath.Join(homedir.HomeDir(), ".kube", "config")
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	ssar := &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace: settings.Namespace,
				Verb:      "create",
				Resource:  "pods",
			},
		},
	}

	review, err := clientset.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, ssar, metav1.CreateOptions{})
	if err != nil {
		log.Printf("⚠️  Warning: Failed to verify permissions (SelfSubjectAccessReview): %v", err)
	} else if !review.Status.Allowed {
		return nil, fmt.Errorf("❌ Missing permission: Cannot create Pods in namespace %q. Please ensure the CI service account has 'create' access to 'pods'.", settings.Namespace)
	}

	privileged := true
	podName := generateUniqueName()
	log.Printf("Creating pod: %s in namespace %s", podName, settings.Namespace)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        podName,
			Namespace:   settings.Namespace,
			Labels:      settings.Labels,
			Annotations: settings.Annotations,
		},
		Spec: corev1.PodSpec{
			HostPID: settings.HostPID,
			Containers: []corev1.Container{
				{
					Name:            "orchestrator",
					Image:           settings.Image,
					ImagePullPolicy: corev1.PullIfNotPresent,
					Command:         settings.Command,
					Args:            settings.Args,
					SecurityContext: &corev1.SecurityContext{
						Privileged: &privileged,
					},
					Ports: []corev1.ContainerPort{
						{Name: "http", ContainerPort: 8080},
						{Name: "grpc", ContainerPort: 9090},
					},
					Env: settings.Env,
				},
			},
		},
	}

	if pod.Labels == nil {
		pod.Labels = make(map[string]string)
	}
	pod.Labels["app"] = "kube-parcel"

	if settings.CPU != "" || settings.Memory != "" {
		resources := corev1.ResourceRequirements{
			Limits: make(corev1.ResourceList),
		}
		if settings.CPU != "" {
			resources.Limits[corev1.ResourceCPU] = resource.MustParse(settings.CPU)
		}
		if settings.Memory != "" {
			resources.Limits[corev1.ResourceMemory] = resource.MustParse(settings.Memory)
		}
		pod.Spec.Containers[0].Resources = resources
	}

	_, err = clientset.CoreV1().Pods(settings.Namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create pod: %w", err)
	}

	var podIP string
	var lastRestartCount int32

	log.Printf("⏳ Waiting for pod %s to be fully ready...", podName)
	err = wait.PollUntilContextTimeout(ctx, 1*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		p, err := clientset.CoreV1().Pods(settings.Namespace).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		if p.Status.Phase == corev1.PodFailed || p.Status.Phase == corev1.PodSucceeded {
			return false, fmt.Errorf("pod reached terminal state: %s", p.Status.Phase)
		}

		if p.Status.Phase == corev1.PodRunning {
			allReady := true
			for _, cs := range p.Status.ContainerStatuses {
				if !cs.Ready {
					allReady = false
					break
				}
				lastRestartCount = cs.RestartCount
			}

			if allReady && p.Status.PodIP != "" {
				podIP = p.Status.PodIP
				return true, nil
			}
		}

		fmt.Print(".")
		return false, nil
	})
	fmt.Println()
	if err != nil {
		return nil, fmt.Errorf("timeout waiting for pod to be ready: %w", err)
	}

	finalPod, err := clientset.CoreV1().Pods(settings.Namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to re-fetch pod IP: %w", err)
	}
	podIP = finalPod.Status.PodIP
	if podIP == "" {
		return nil, fmt.Errorf("pod IP is empty after pod became ready")
	}
	log.Printf("📍 Confirmed stable pod IP: %s (restarts: %d)", podIP, lastRestartCount)

	url := fmt.Sprintf("http://localhost:%d", parcelconfig.DefaultHTTPPort)
	inCluster := false
	if _, err := rest.InClusterConfig(); err == nil {
		inCluster = true
		url = fmt.Sprintf("http://%s:%d", podIP, parcelconfig.DefaultHTTPPort)
		log.Printf("✅ Running in-cluster, using Pod IP: %s", url)
	}
	if !inCluster {
		log.Printf("👉 Please run: kubectl port-forward pod/%s %d:%d -n %s", podName, parcelconfig.DefaultHTTPPort, parcelconfig.DefaultHTTPPort, settings.Namespace)
	}

	log.Printf("✅ Pod is running!")

	handle := &ServerHandle{
		mode: "remote",
		url:  url,
		cleanup: func() error {
			log.Println("Stopping remote pod...")
			return clientset.CoreV1().Pods(settings.Namespace).Delete(ctx, podName, metav1.DeleteOptions{})
		},
	}

	log.Printf("Waiting for server readiness (polling %s)...", url)
	if err := waitForServer(ctx, url); err != nil {
		if !inCluster {
			return nil, fmt.Errorf("remote server failed to become ready (did you start port-forwarding?): %w", err)
		}
		return nil, fmt.Errorf("remote server failed to become ready at %s: %w", url, err)
	}

	if inCluster {
		log.Println("⏳ Waiting for pod to stabilize (monitoring restarts)...")
		stableChecks := 0
		lastRestarts := int32(-1)

		err := wait.PollUntilContextTimeout(ctx, 1*time.Second, 30*time.Second, true, func(ctx context.Context) (bool, error) {
			p, err := clientset.CoreV1().Pods(settings.Namespace).Get(ctx, podName, metav1.GetOptions{})
			if err != nil {
				return false, fmt.Errorf("failed to check pod stability: %w", err)
			}

			currentRestarts := int32(0)
			for _, cs := range p.Status.ContainerStatuses {
				currentRestarts += cs.RestartCount
			}

			if currentRestarts == lastRestarts {
				stableChecks++
				if stableChecks >= 3 {
					newIP := p.Status.PodIP
					if newIP != "" && newIP != podIP {
						log.Printf("⚠️ Pod IP changed: %s → %s", podIP, newIP)
						url = fmt.Sprintf("http://%s:%d", newIP, parcelconfig.DefaultHTTPPort)
						handle.url = url

						log.Printf("🔄 Verifying new pod IP: %s...", url)
						if err := waitForServer(ctx, url); err != nil {
							return false, fmt.Errorf("server at new IP %s failed: %w", url, err)
						}
					}
					log.Printf("✅ Pod stable (restarts: %d)", currentRestarts)
					return true, nil
				}
			} else {
				stableChecks = 0
				lastRestarts = currentRestarts
				log.Printf("🔄 Pod restart detected (restarts: %d), waiting...", currentRestarts)
			}
			return false, nil
		})
		if err != nil {
			log.Printf("⚠️ Pod stability check timed out, continuing anyway: %v", err)
		}
	}

	return handle, nil

}

func waitForServer(ctx context.Context, baseURL string) error {
	httpClient := &http.Client{
		Timeout: 2 * time.Second,
	}
	url := fmt.Sprintf("%s/parcel/status", baseURL)

	log.Printf("Polling %s...", baseURL)

	err := wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, parcelconfig.ServerReadinessTimeout, true, func(ctx context.Context) (bool, error) {
		resp, err := httpClient.Get(url)
		if err != nil {
			fmt.Print(".") // Visual feedback
			return false, nil
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			fmt.Println()
			log.Println("✅ Server is ready!")
			return true, nil
		}

		fmt.Print(".")
		return false, nil
	})

	if err != nil {
		return fmt.Errorf("timeout waiting for server: %w", err)
	}

	return nil
}
