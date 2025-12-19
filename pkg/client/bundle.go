package client

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/crane"
	"gopkg.in/yaml.v3"
)

// Image source prefixes
const (
	PrefixOCI    = "oci://"     // OCI directory
	PrefixTar    = "tar://"     // Docker tar file
	PrefixOCITar = "oci-tar://" // Docker tar file (alias)
	PrefixRemote = "remote://"  // Remote registry (using crane)
)

// Bundler creates tar-in-tar bundles from charts and images
type Bundler struct {
	chartDirs  []string
	imagePaths []string // Paths with prefixes: oci://, tar://, remote://
}

// NewBundler creates a new bundler for charts and images
func NewBundler(chartDirs []string, imagePaths []string) *Bundler {
	return &Bundler{
		chartDirs:  chartDirs,
		imagePaths: imagePaths,
	}
}

// Bundle creates a tar stream containing images and charts
func (b *Bundler) Bundle(ctx context.Context, w io.Writer) error {
	log.Printf("📦 Bundling %d chart(s) and %d image(s)", len(b.chartDirs), len(b.imagePaths))

	tw := tar.NewWriter(w)
	defer tw.Close()

	for _, imageSpec := range b.imagePaths {
		if err := b.addImageFromSpec(ctx, tw, imageSpec); err != nil {
			log.Printf("Warning: failed to add image %s: %v", imageSpec, err)
		}
	}

	for _, chartDir := range b.chartDirs {
		log.Printf("Processing chart: %s", chartDir)

		if err := b.addChartTo(tw, chartDir); err != nil {
			log.Printf("Warning: failed to add chart %s: %v", chartDir, err)
		}
	}

	log.Println("✅ Bundle creation complete")
	return nil
}

// addImageFromSpec adds an image based on its prefix
func (b *Bundler) addImageFromSpec(ctx context.Context, tw *tar.Writer, imageSpec string) error {
	var tag string
	// Check for tag=path prefix
	if parts := strings.SplitN(imageSpec, "=", 2); len(parts) == 2 {
		if strings.HasPrefix(parts[1], "/") ||
			strings.HasPrefix(parts[1], PrefixOCI) ||
			strings.HasPrefix(parts[1], PrefixTar) ||
			strings.HasPrefix(parts[1], PrefixOCITar) ||
			strings.HasPrefix(parts[1], PrefixRemote) {
			tag = parts[0]
			imageSpec = parts[1]
		}
	}

	switch {
	case strings.HasPrefix(imageSpec, PrefixOCI):
		path := strings.TrimPrefix(imageSpec, PrefixOCI)
		return b.addOCIDirectory(tw, path, tag)

	case strings.HasPrefix(imageSpec, PrefixTar):
		path := strings.TrimPrefix(imageSpec, PrefixTar)
		return b.addImageTar(tw, path)

	case strings.HasPrefix(imageSpec, PrefixOCITar):
		path := strings.TrimPrefix(imageSpec, PrefixOCITar)
		return b.addImageTar(tw, path)

	case strings.HasPrefix(imageSpec, PrefixRemote):
		ref := strings.TrimPrefix(imageSpec, PrefixRemote)
		return b.addRemoteImage(ctx, tw, ref)

	default:
		return b.addImageFromPath(tw, imageSpec, tag)
	}
}

// addImageFromPath auto-detects the image type from path
func (b *Bundler) addImageFromPath(tw *tar.Writer, imagePath, tag string) error {
	info, err := os.Stat(imagePath)
	if err != nil {
		return fmt.Errorf("image path not found: %w", err)
	}

	if info.IsDir() {
		return b.addOCIDirectory(tw, imagePath, tag)
	} else if strings.HasSuffix(imagePath, ".tar") {
		return b.addImageTar(tw, imagePath)
	}

	return fmt.Errorf("unsupported image format: %s (expected .tar file or OCI directory, or use oci://, oci-tar://, remote:// prefix)", imagePath)
}

// addImageTar adds an existing image tar file to the bundle
func (b *Bundler) addImageTar(tw *tar.Writer, tarPath string) error {
	log.Printf("Adding image tar: %s", tarPath)

	file, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return err
	}

	tarName := filepath.Base(tarPath)

	header := &tar.Header{
		Name: tarName,
		Size: stat.Size(),
		Mode: 0644,
	}

	if err := tw.WriteHeader(header); err != nil {
		return err
	}

	written, err := io.Copy(tw, file)
	if err != nil {
		return err
	}

	log.Printf("✅ Added image: %s (%d bytes)", tarName, written)
	return nil
}

// addOCIDirectory tars an OCI directory and adds it to the bundle
func (b *Bundler) addOCIDirectory(tw *tar.Writer, ociDir, tag string) error {
	log.Printf("Adding OCI directory: %s (tag: %s)", ociDir, tag)

	tmpFile, err := os.CreateTemp("", "oci-*.tar")
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())

	ociTw := tar.NewWriter(tmpFile)
	err = filepath.Walk(ociDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(ociDir, path)
		if err != nil {
			return err
		}

		if info.Mode()&os.ModeSymlink != 0 {
			realPath, err := filepath.EvalSymlinks(path)
			if err != nil {
				return err
			}
			realInfo, err := os.Stat(realPath)
			if err != nil {
				return err
			}
			info = realInfo
			path = realPath
		}

		if tag != "" && relPath == "index.json" {
			return b.writeModifiedIndex(ociTw, path, tag)
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = relPath

		if err := ociTw.WriteHeader(header); err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(ociTw, file)
		return err
	})
	if err != nil {
		ociTw.Close()
		return fmt.Errorf("failed to tar OCI directory: %w", err)
	}
	ociTw.Close()
	tmpFile.Close()

	tarName := filepath.Base(ociDir) + ".tar"
	if tag != "" {
		tarName = strings.ReplaceAll(tag, ":", "_") + ".tar"
		tarName = strings.ReplaceAll(tarName, "/", "_")
	}
	return b.addImageTarWithName(tw, tmpFile.Name(), tarName)
}

// writeModifiedIndex reads the index.json, injects the tag annotation, and writes to tar
func (b *Bundler) writeModifiedIndex(tw *tar.Writer, indexPath, tag string) error {
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return err
	}

	var index map[string]interface{}
	if err := json.Unmarshal(data, &index); err != nil {
		return fmt.Errorf("failed to parse index.json: %w", err)
	}

	if manifests, ok := index["manifests"].([]interface{}); ok && len(manifests) > 0 {
		if manifest, ok := manifests[0].(map[string]interface{}); ok {
			annotations, ok := manifest["annotations"].(map[string]interface{})
			if !ok {
				annotations = make(map[string]interface{})
				manifest["annotations"] = annotations
			}
			annotations["org.opencontainers.image.ref.name"] = tag
		}
	}

	modifiedData, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}

	header := &tar.Header{
		Name: "index.json",
		Size: int64(len(modifiedData)),
		Mode: 0644,
	}

	if err := tw.WriteHeader(header); err != nil {
		return err
	}

	_, err = tw.Write(modifiedData)
	return err
}

// addRemoteImage pulls an image from a remote registry and adds it to the bundle
func (b *Bundler) addRemoteImage(ctx context.Context, tw *tar.Writer, imageRef string) error {
	log.Printf("Pulling remote image: %s", imageRef)

	tmpFile, err := os.CreateTemp("", "remote-img-*.tar")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close() // crane.Save needs path
	defer os.Remove(tmpPath)

	img, err := crane.Pull(imageRef)
	if err != nil {
		return fmt.Errorf("failed to pull image %s: %w", imageRef, err)
	}

	// Save as a Docker-compatible tarball
	// We use the original image ref as the tag in the tar
	// Signature: Save(img v1.Image, tag, path string)
	err = crane.Save(img, imageRef, tmpPath)
	if err != nil {
		return fmt.Errorf("failed to save image tar: %w", err)
	}

	// Add to bundle
	// Normalize name for tar entry (replace : and / with _)
	tarName := strings.ReplaceAll(imageRef, ":", "_") + ".tar"
	tarName = strings.ReplaceAll(tarName, "/", "_")

	return b.addImageTarWithName(tw, tmpPath, tarName)
}

// addImageTarWithName adds a tar file to the bundle with a custom name
func (b *Bundler) addImageTarWithName(tw *tar.Writer, tarPath, tarName string) error {
	file, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return err
	}

	header := &tar.Header{
		Name: tarName,
		Size: stat.Size(),
		Mode: 0644,
	}

	if err := tw.WriteHeader(header); err != nil {
		return err
	}

	written, err := io.Copy(tw, file)
	if err != nil {
		return err
	}

	log.Printf("✅ Added image: %s (%d bytes)", tarName, written)
	return nil
}

// addChartTo adds a chart directory to the tar
func (b *Bundler) addChartTo(tw *tar.Writer, chartDir string) error {
	log.Printf("Adding chart directory: %s", chartDir)

	return filepath.Walk(chartDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Create relative path
		relPath, err := filepath.Rel(chartDir, path)
		if err != nil {
			return err
		}

		// Skip root
		if relPath == "." {
			return nil
		}

		// Dereference symlinks (Bazel runfiles are symlinks)
		if info.Mode()&os.ModeSymlink != 0 {
			targetInfo, err := os.Stat(path)
			if err != nil {
				return fmt.Errorf("failed to stat symlink target %s: %w", path, err)
			}
			info = targetInfo
		}

		// Prefix with charts/CHARTNAME/
		chartName := filepath.Base(chartDir)
		tarPath := filepath.Join("charts", chartName, relPath)

		// Create header
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = tarPath

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		// Write file content
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(tw, file)
		return err
	})
}

// ExtractImagesFromChart extracts image references from a chart's values.yaml
// This is exported for callers who want to discover which images need to be provided
func ExtractImagesFromChart(chartDir string) ([]string, error) {
	valuesPath := filepath.Join(chartDir, "values.yaml")

	data, err := os.ReadFile(valuesPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var values map[string]interface{}
	if err := yaml.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("failed to parse values.yaml: %w", err)
	}

	var images []string
	extractImagesRecursive(values, &images)

	return images, nil
}

// extractImagesRecursive recursively extracts image references from a values tree
func extractImagesRecursive(v interface{}, images *[]string) {
	switch val := v.(type) {
	case map[string]interface{}:
		if repo, ok := val["repository"].(string); ok {
			tag := "latest"
			if t, ok := val["tag"].(string); ok {
				tag = t
			}
			*images = append(*images, fmt.Sprintf("%s:%s", repo, tag))
		}
		for _, value := range val {
			extractImagesRecursive(value, images)
		}
	case []interface{}:
		for _, val := range val {
			extractImagesRecursive(val, images)
		}
	}
}
