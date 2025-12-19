package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tiborv/kube-parcel/pkg/client"
	"github.com/tiborv/kube-parcel/pkg/config"
	"github.com/tiborv/kube-parcel/pkg/shared"
)

var (
	cfgFile string
	rootCmd = &cobra.Command{
		Use:     "kube-parcel",
		Short:   "CI-first integration testing for Helm charts",
		Long:    `kube-parcel - Run a full K3s cluster in a container for airgapped Helm chart testing`,
		Version: config.Version,
	}
)

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: $HOME/.kube-parcel.yaml)")

	startCmd := &cobra.Command{
		Use:   "start [chart-dirs...]",
		Short: "Launch server and upload charts",
		Long:  `Launch an ephemeral K3s server (locally via Docker or remotely in Kubernetes) and run tests`,
		Args:  cobra.MinimumNArgs(1),
		Run:   runStart,
	}
	startCmd.Flags().String("exec-mode", "docker", "Execution mode: 'docker' (local) or 'k8s' (Kubernetes cluster)")
	startCmd.Flags().String("namespace", "default", "Kubernetes namespace (for remote mode)")
	startCmd.Flags().String("runner-image", "ghcr.io/tiborv/kube-parcel-runner:v"+config.MinorVersion, "Runner image to use")
	startCmd.Flags().String("cpu", "", "CPU limit (e.g., 1000m)")
	startCmd.Flags().String("memory", "", "Memory limit (e.g., 2Gi)")
	startCmd.Flags().String("labels", "", "Comma-separated labels (key=value)")
	startCmd.Flags().String("annotations", "", "Comma-separated annotations (key=value)")
	startCmd.Flags().Bool("host-pid", true, "Use host PID namespace for better nested container support (default: true)")
	startCmd.Flags().Bool("keep-alive", false, "Keep container running after tests complete")
	startCmd.Flags().Bool("no-airgap", false, "Disable airgap mode (allow K3s to pull external images)")
	startCmd.Flags().StringSlice("load-images", nil, "Image tars or OCI directories to load into the cluster")
	viper.BindPFlags(startCmd.Flags())
	rootCmd.AddCommand(startCmd)

	uploadCmd := &cobra.Command{
		Use:   "upload [chart-dirs...]",
		Short: "Upload charts to existing server",
		Args:  cobra.MinimumNArgs(1),
		Run:   runUpload,
	}
	uploadCmd.Flags().String("server", "http://localhost:8080", "Server URL")
	viper.BindPFlags(uploadCmd.Flags())
	rootCmd.AddCommand(uploadCmd)

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Check server status",
		Run:   runStatus,
	}
	statusCmd.Flags().String("server", "http://localhost:8080", "Server URL")
	viper.BindPFlags(statusCmd.Flags())
	rootCmd.AddCommand(statusCmd)
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		if err == nil {
			viper.AddConfigPath(home)
		}
		viper.SetConfigName(".kube-parcel")
		viper.SetConfigType("yaml")
	}

	viper.SetEnvPrefix("KUBE_PARCEL")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()

	viper.ReadInConfig() // Ignore errors if config file doesn't exist
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runStart(cmd *cobra.Command, args []string) {
	ctx := context.Background()
	chartDirs := args

	execMode, _ := cmd.Flags().GetString("exec-mode")
	image, _ := cmd.Flags().GetString("runner-image")
	keepAlive, _ := cmd.Flags().GetBool("keep-alive")
	noAirgap, _ := cmd.Flags().GetBool("no-airgap")
	imagePaths, _ := cmd.Flags().GetStringSlice("load-images")

	var handle *client.ServerHandle
	var err error

	env := make(map[string]string)
	if noAirgap {
		env["KUBE_PARCEL_AIRGAP"] = "false"
	}

	if execMode == "docker" {
		handle, err = client.LaunchLocal(ctx, image, env)
	} else {
		namespace, _ := cmd.Flags().GetString("namespace")
		cpu, _ := cmd.Flags().GetString("cpu")
		memory, _ := cmd.Flags().GetString("memory")
		labels, _ := cmd.Flags().GetString("labels")
		annotations, _ := cmd.Flags().GetString("annotations")
		hostPID, _ := cmd.Flags().GetBool("host-pid")

		settings := client.PodSettings{
			Namespace:   namespace,
			Image:       image,
			CPU:         cpu,
			Memory:      memory,
			Labels:      parseMap(labels),
			Annotations: parseMap(annotations),
			HostPID:     hostPID,
		}
		handle, err = client.LaunchRemote(ctx, settings)
	}

	if err != nil {
		log.Fatalf("❌ Failed to launch server: %v", err)
	}

	// Only cleanup if not keeping alive or if tests pass
	testFailed := false
	defer func() {
		if keepAlive && testFailed {
			log.Println("🔒 Container kept alive for debugging")
			log.Printf("   URL: %s", handle.URL())
			return
		}
		handle.Cleanup()
	}()

	if err := uploadToServer(ctx, handle.URL(), chartDirs, imagePaths); err != nil {
		log.Fatalf("❌ Upload failed: %v", err)
	}

	if err := client.StreamLogs(ctx, handle.URL()); err != nil {
		testFailed = true
		log.Printf("❌ Tests failed")
		os.Exit(1)
	}
}

func runUpload(cmd *cobra.Command, args []string) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	serverURL, _ := cmd.Flags().GetString("server")

	if err := uploadToServer(ctx, serverURL, args, nil); err != nil {
		log.Fatalf("❌ Upload failed: %v", err)
	}

	if err := client.StreamLogs(ctx, serverURL); err != nil {
		log.Printf("❌ Tests failed")
		os.Exit(1)
	}
}

func runStatus(cmd *cobra.Command, args []string) {
	serverURL, _ := cmd.Flags().GetString("server")

	resp, err := http.Get(serverURL + "/parcel/status")
	if err != nil {
		log.Fatalf("❌ Failed to fetch status: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Fatalf("❌ Server returned error: %d", resp.StatusCode)
	}

	var status shared.StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		log.Fatalf("❌ Failed to decode status: %v", err)
	}

	fmt.Printf("🌐 Server State: %s (Uptime: %ds)\n", status.State, status.Uptime)
	fmt.Printf("☸️ Cluster Status: %s (K3s Ready: %v)\n", status.ClusterStatus, status.K3sReady)
	fmt.Printf("📦 Content: %d Images, %d Charts\n", status.ImagesCount, status.ChartsCount)

	if len(status.Charts) > 0 {
		fmt.Println("\n🪖 Helm Charts:")
		for name, chart := range status.Charts {
			icon := "⏳"
			switch chart.Phase {
			case "Succeeded":
				icon = "🎉"
			case "Failed":
				icon = "❌"
			case "Deployed":
				icon = "✅"
			case "Testing":
				icon = "🧪"
			}
			fmt.Printf("  %s %-15s [%s] %s\n", icon, name, chart.Phase, chart.Message)
		}
	}
}

func uploadToServer(ctx context.Context, serverURL string, chartDirs []string, imagePaths []string) error {
	fmt.Printf("📤 Streaming to: %s/parcel/upload\n", serverURL)

	bundler := client.NewBundler(chartDirs, imagePaths)
	pr, pw := client.NewPipe()

	go func() {
		defer pw.Close()
		if err := bundler.Bundle(ctx, pw); err != nil {
			log.Printf("❌ Bundling error: %v", err)
		}
	}()

	req, err := http.NewRequestWithContext(ctx, "POST", serverURL+"/parcel/upload", pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-tar")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}

	fmt.Println("✅ Upload accepted")
	return nil
}

func parseMap(s string) map[string]string {
	if s == "" {
		return nil
	}
	res := make(map[string]string)
	parts := strings.Split(s, ",")
	for _, p := range parts {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) == 2 {
			res[kv[0]] = kv[1]
		}
	}
	return res
}
