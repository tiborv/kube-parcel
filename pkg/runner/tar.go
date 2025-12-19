package runner

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tiborv/kube-parcel/pkg/config"
)

// ImportImages looks for any tarballs in the images directory and imports them into K3s
func ImportImages() error {
	log.Printf("🔍 Scanning images directory: %s", config.DefaultImagesDir)

	err := filepath.Walk(config.DefaultImagesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Printf("Error accessing path %s: %v", path, err)
			return err
		}

		if info.IsDir() {
			return nil
		}

		name := info.Name()
		if !strings.HasSuffix(name, ".tar") && !strings.HasSuffix(name, ".tar.gz") && !strings.HasSuffix(name, ".tgz") {
			return nil
		}

		log.Printf("📦 Importing image: %s", name)

		f, err := os.Open(path)
		if err != nil {
			log.Printf("Warning: failed to open %s: %v", name, err)
			return nil
		}
		defer f.Close()

		var r io.Reader = f
		if strings.HasSuffix(name, ".gz") || strings.HasSuffix(name, ".tgz") {
			gz, err := gzip.NewReader(f)
			if err != nil {
				log.Printf("Warning: failed to create gzip reader for %s: %v", name, err)
				return nil
			}
			defer gz.Close()
			r = gz
		}

		// Use ctr to import into containerd (K3s uses k3s ctr)
		// We pipe the reader to stdin and use '-' as filename for import
		ctx, cancel := context.WithTimeout(context.Background(), config.ImageImportTimeout)
		defer cancel()

		cmd := exec.CommandContext(ctx, "ctr", "-a", config.ContainerdSocket,
			"-n", config.ContainerdNamespace, "images", "import", "-")
		cmd.Stdin = r

		output, err := cmd.CombinedOutput()
		if err != nil {
			log.Printf("Warning: failed to import %s: %v (output: %s)", name, err, string(output))
			return nil // Continue walking
		}
		log.Printf("✅ Imported image: %s", name)

		// Normalize tags: if image has a short name (no registry prefix), add docker.io/library/ prefix
		// This fixes ErrImageNeverPull because Kubernetes normalizes short names to docker.io/library/
		normalizeImageTags()

		return nil
	})

	return err
}

// normalizeImageTags adds docker.io/library/ prefix to images with short names
func normalizeImageTags() {
	listCmd := exec.Command("ctr", "-a", config.ContainerdSocket,
		"-n", config.ContainerdNamespace, "images", "list", "-q")
	output, err := listCmd.Output()
	if err != nil {
		log.Printf("Warning: failed to list images for normalization: %v", err)
		return
	}

	images := strings.Split(string(output), "\n")
	for _, img := range images {
		img = strings.TrimSpace(img)
		if img == "" || strings.HasPrefix(img, "sha256:") {
			continue
		}

		// Check if image needs docker.io/library/ prefix
		// Images like "kube-parcel-test:latest" need to become "docker.io/library/kube-parcel-test:latest"
		if !strings.Contains(img, "/") && !strings.HasPrefix(img, "docker.io") {
			targetTag := "docker.io/library/" + img
			tagCmd := exec.Command("ctr", "-a", config.ContainerdSocket,
				"-n", config.ContainerdNamespace, "images", "tag", img, targetTag)
			if tagOut, err := tagCmd.CombinedOutput(); err != nil {
				log.Printf("Warning: failed to add normalized tag %s: %v (output: %s)", targetTag, err, string(tagOut))
			} else {
				log.Printf("🏷️  Tagged %s → %s", img, targetTag)
			}
		}
	}
}

// TarExtractor handles tar-in-tar stream extraction
type TarExtractor struct {
	imagesDir string
	chartsDir string
	onImage   func(name string)
	onChart   func(name string)
}

// NewTarExtractor creates a new extractor
func NewTarExtractor() *TarExtractor {
	return &TarExtractor{
		imagesDir: config.DefaultImagesDir,
		chartsDir: config.DefaultChartsDir,
	}
}

// OnImage registers a callback when an image is extracted
func (te *TarExtractor) OnImage(fn func(name string)) {
	te.onImage = fn
}

// OnChart registers a callback when a chart is extracted
func (te *TarExtractor) OnChart(fn func(name string)) {
	te.onChart = fn
}

// Extract processes the tar-in-tar stream
func (te *TarExtractor) Extract(r io.Reader) error {
	if err := os.MkdirAll(te.imagesDir, 0755); err != nil {
		return fmt.Errorf("failed to create images dir: %w", err)
	}
	if err := os.MkdirAll(te.chartsDir, 0755); err != nil {
		return fmt.Errorf("failed to create charts dir: %w", err)
	}

	tr := tar.NewReader(r)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read error: %w", err)
		}

		if te.isImageTar(header.Name) {
			if err := te.extractImage(tr, header); err != nil {
				log.Printf("Warning: failed to extract image %s: %v", header.Name, err)
				continue
			}
			if te.onImage != nil {
				te.onImage(header.Name)
			}
		} else if te.isChartFile(header.Name) {
			if err := te.extractChart(tr, header); err != nil {
				log.Printf("Warning: failed to extract chart file %s: %v", header.Name, err)
				continue
			}
		}
	}

	return nil
}

// isImageTar checks if the file is a Docker image tar
func (te *TarExtractor) isImageTar(name string) bool {
	return (strings.HasSuffix(name, ".tar") || strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tgz")) && !strings.Contains(name, "/")
}

// isChartFile checks if the file belongs to a Helm chart
func (te *TarExtractor) isChartFile(name string) bool {
	// Files under charts/ directory or containing Chart.yaml
	return strings.HasPrefix(name, "charts/") || strings.Contains(name, "Chart.yaml")
}

// extractImage extracts an image tar to the images directory
func (te *TarExtractor) extractImage(r io.Reader, header *tar.Header) error {
	targetPath := filepath.Join(te.imagesDir, filepath.Base(header.Name))

	outFile, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, r); err != nil {
		return err
	}

	log.Printf("Extracted image: %s -> %s", header.Name, targetPath)
	return nil
}

// extractChart extracts a chart file to the charts directory
func (te *TarExtractor) extractChart(r io.Reader, header *tar.Header) error {
	relativePath := strings.TrimPrefix(header.Name, "charts/")
	targetPath := filepath.Join(te.chartsDir, relativePath)

	if header.Typeflag == tar.TypeDir {
		if err := os.MkdirAll(targetPath, 0755); err != nil {
			return err
		}
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return err
	}

	outFile, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, r); err != nil {
		return err
	}

	// Notify on Chart.yaml to track chart count
	if filepath.Base(header.Name) == "Chart.yaml" && te.onChart != nil {
		chartName := filepath.Base(filepath.Dir(targetPath))
		te.onChart(chartName)
	}

	return nil
}
