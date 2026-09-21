package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	logging "github.com/ipfs/go-log/v2"
	"gorm.io/gorm"
)

const (
	targetPath = "usr/local/bin/mina"
	cacheDir   = "mina-executable"
)

type TokenResponse struct {
	Token string `json:"token"`
}

type Manifest struct {
	Layers []Layer `json:"layers"`
}

type Layer struct {
	Digest string `json:"digest"`
}

// getLatestDeploymentRelease queries the database for the latest deployment release
func getLatestDeploymentRelease(db *gorm.DB) (string, error) {
	var release sql.NullString

	err := db.Raw(`
		SELECT metadata_json->>'release' as release
		FROM deployment
		ORDER BY deployment_id DESC
		LIMIT 1
	`).Scan(&release).Error

	if err != nil {
		return "", fmt.Errorf("failed to query deployment release: %w", err)
	}

	if !release.Valid || release.String == "" {
		return "", fmt.Errorf("no release found in deployment metadata")
	}

	return release.String, nil
}

// The registry calls used to run on &http.Client{} and http.DefaultClient,
// both of which have Timeout: 0. If egress was blackholed after the TCP
// connect, the extractor blocked in Read forever; it runs from loadRun in a
// goroutine, so POST /experiment/run had already returned 200, the experiment
// sat at "running" reporting nothing, and Store.Add 409'd every later create
// until the pod was restarted.
var (
	// metadataClient covers the token and manifest calls, which are small.
	metadataClient = &http.Client{Timeout: 30 * time.Second}
	// blobClient covers layer downloads, which are hundreds of megabytes.
	blobClient = &http.Client{Timeout: 30 * time.Minute}
)

// osCodenames are the Debian/Ubuntu codenames that can appear in a release tag.
var osCodenames = map[string]bool{
	"bullseye": true, "bookworm": true, "buster": true,
	"focal": true, "jammy": true, "noble": true,
}

// processReleaseString rewrites a release tag to its bullseye build.
//
// Only a segment that actually names an OS codename is rewritten. Rewriting
// the second-to-last segment unconditionally assumed every tag ends in
// "<codename>-<network>", which most do not:
//
//	3.2.0-alpha1-app-state32-05da85d
//	  -> 3.2.0-alpha1-app-bullseye-05da85d      (corrupted)
//	3.4.0-alpha1-mesa-mut-prefork-cac0e3e-bullseye-mesa-mut-generic
//	  -> ...-bullseye-mesa-bullseye-generic     (corrupted)
//
// A corrupted tag 404s at the manifest fetch, and the caller then warns and
// silently falls back to the bundled client -- so the feature was a no-op on
// the tags actually in use.
func processReleaseString(release string) string {
	parts := strings.Split(release, "-")

	// Search from the end: the codename sits in the suffix, and an earlier
	// segment could coincidentally match (a branch named "focal", say).
	for i := len(parts) - 1; i >= 0; i-- {
		if osCodenames[parts[i]] {
			parts[i] = "bullseye"
			return strings.Join(parts, "-")
		}
	}

	// No codename found -- the tag is not in a shape we understand, so leave
	// it alone rather than corrupting it.
	return release
}

// getMinaExecutablePath returns the path to the cached Mina executable, extracting it if necessary
func getMinaExecutablePath(ctx context.Context, db *gorm.DB, log logging.StandardLogger) (string, error) {
	// Get the latest deployment release
	release, err := getLatestDeploymentRelease(db)
	if err != nil {
		return "", fmt.Errorf("failed to get deployment release: %w", err)
	}

	// Process the release string to ensure bullseye
	processedRelease := processReleaseString(release)

	// Create cache directory if it doesn't exist
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Check if executable is already cached
	executableName := fmt.Sprintf("mina-%s", processedRelease)
	executablePath := filepath.Join(cacheDir, executableName)

	if _, err := os.Stat(executablePath); err == nil {
		// Executable already exists in cache
		log.Infof("Using cached Mina executable: %s", executableName)
		return filepath.Abs(executablePath)
	}

	// Extract executable from Docker image
	dockerImage := fmt.Sprintf("europe-west3-docker.pkg.dev/o1labs-192920/euro-docker-repo/mina-daemon:%s", processedRelease)
	log.Infof("Extracting Mina executable from Docker image: %s", dockerImage)

	if err := extractMinaBinary(ctx, dockerImage, executablePath, log); err != nil {
		return "", fmt.Errorf("failed to extract mina binary: %w", err)
	}

	return filepath.Abs(executablePath)
}

// extractMinaBinary extracts the mina binary from a Docker image
func extractMinaBinary(ctx context.Context, dockerImage, outputFile string, log logging.StandardLogger) error {
	log.Infof("Starting extraction of mina binary from Docker image: %s", dockerImage)

	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "mina-extract-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer func() {
		os.RemoveAll(tempDir)
	}()

	// Get registry token
	log.Debugf("Getting registry token for Docker image extraction")
	token, err := getRegistryToken(ctx, log)
	if err != nil {
		return fmt.Errorf("failed to get registry token: %w", err)
	}
	log.Debugf("Registry token obtained successfully")

	// Get image manifest
	log.Debugf("Getting image manifest for %s", dockerImage)
	manifest, err := getImageManifest(ctx, token, dockerImage, log)
	if err != nil {
		return fmt.Errorf("failed to get image manifest: %w", err)
	}
	log.Debugf("Image manifest obtained successfully, found %d layers", len(manifest.Layers))

	// Download and search layers
	log.Infof("Searching for mina binary in %d layers", len(manifest.Layers))
	for i, layer := range manifest.Layers {
		log.Debugf("Processing layer %d/%d: %s", i+1, len(manifest.Layers), layer.Digest)
		if found, err := processLayer(ctx, token, layer.Digest, tempDir, i+1, outputFile, dockerImage, log); err != nil {
			log.Warnf("Failed to process layer %d: %v", i+1, err)
			continue
		} else if found {
			// extractLayer chmods before renaming into place, so the file is
			// already executable by the time it exists at outputFile.
			log.Infof("Successfully extracted mina binary to: %s", outputFile)
			return nil
		}
	}

	return fmt.Errorf("mina binary not found in any layer")
}

func getRegistryToken(ctx context.Context, log logging.StandardLogger) (string, error) {
	tokenURL := "https://europe-west3-docker.pkg.dev/v2/token?service=europe-west3-docker.pkg.dev&scope=repository:o1labs-192920/euro-docker-repo/mina-daemon:pull"

	req, err := http.NewRequestWithContext(ctx, "GET", tokenURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := metadataClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token request failed with status: %s", resp.Status)
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", err
	}

	if tokenResp.Token == "" {
		return "", fmt.Errorf("empty token received")
	}

	return tokenResp.Token, nil
}

func getImageManifest(ctx context.Context, token, dockerImage string, log logging.StandardLogger) (*Manifest, error) {
	// Parse the docker image to extract repository and tag
	parts := strings.Split(dockerImage, ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid docker image format: %s", dockerImage)
	}

	repository := parts[0]
	tag := parts[1]

	// Remove registry prefix for the API call
	repo := strings.TrimPrefix(repository, "europe-west3-docker.pkg.dev/")

	manifestURL := fmt.Sprintf("https://europe-west3-docker.pkg.dev/v2/%s/manifests/%s", repo, tag)

	log.Infof("Fetching image manifest: url=%s tag=%s", manifestURL, tag)

	req, err := http.NewRequestWithContext(ctx, "GET", manifestURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json")

	resp, err := metadataClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manifest request failed with status: %s (url=%s, tag=%s)", resp.Status, manifestURL, tag)
	}

	var manifest Manifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return nil, err
	}

	return &manifest, nil
}

func processLayer(ctx context.Context, token, digest, tempDir string, layerNum int, outputFile, dockerImage string, log logging.StandardLogger) (bool, error) {
	// Parse repository from dockerImage for blob URL
	parts := strings.Split(dockerImage, ":")
	repository := parts[0]
	repo := strings.TrimPrefix(repository, "europe-west3-docker.pkg.dev/")

	// Download layer
	blobURL := fmt.Sprintf("https://europe-west3-docker.pkg.dev/v2/%s/blobs/%s", repo, digest)
	layerFile := filepath.Join(tempDir, fmt.Sprintf("layer_%d.tar.gz", layerNum))

	if err := downloadLayer(ctx, token, blobURL, layerFile, log); err != nil {
		return false, fmt.Errorf("failed to download layer: %w", err)
	}
	defer os.Remove(layerFile)

	// Extract layer and search for mina binary
	return extractLayer(layerFile, outputFile, log)
}

func downloadLayer(ctx context.Context, token, blobURL, outputPath string, log logging.StandardLogger) error {
	log.Debugf("Downloading layer from: %s", blobURL)
	req, err := http.NewRequestWithContext(ctx, "GET", blobURL, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := blobClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status: %s", resp.Status)
	}

	outFile, err := os.Create(outputPath)
	if err != nil {
		return err
	}

	if _, err := io.Copy(outFile, resp.Body); err != nil {
		outFile.Close()
		return err
	}
	// Checked rather than deferred: a failed flush here would otherwise be
	// reported as a complete layer and searched as if it were one.
	return outFile.Close()
}

func extractLayer(layerFile, outputFile string, log logging.StandardLogger) (bool, error) {
	file, err := os.Open(layerFile)
	if err != nil {
		return false, err
	}
	defer file.Close()

	// Try to detect if it's gzipped by reading magic bytes
	var reader io.Reader = file

	file.Seek(0, 0)
	header := make([]byte, 2)
	if n, _ := file.Read(header); n == 2 && header[0] == 0x1f && header[1] == 0x8b {
		// File is gzipped
		file.Seek(0, 0)
		gzReader, err := gzip.NewReader(file)
		if err != nil {
			return false, fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer gzReader.Close()
		reader = gzReader
	} else {
		// File is not gzipped, treat as plain tar
		file.Seek(0, 0)
	}

	tarReader := tar.NewReader(reader)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return false, fmt.Errorf("failed to read tar header: %w", err)
		}

		// Check if this is the mina binary we're looking for
		if header.Name == targetPath || strings.HasSuffix(header.Name, "/"+targetPath) {
			if header.Typeflag == tar.TypeReg {
				log.Infof("Found mina binary in layer at path: %s", header.Name)

				// Write to a temporary file and rename into place. Writing
				// straight to outputFile meant a short read or a mid-copy
				// failure left a partial file at the cache path; cache
				// validity is a bare os.Stat, so every later run then
				// reported "Using cached Mina executable" and pointed
				// MinaExec at a truncated, non-executable file -- for good,
				// until someone deleted it by hand.
				tmpPath := fmt.Sprintf("%s.tmp-%d", outputFile, os.Getpid())
				defer os.Remove(tmpPath)

				outFile, err := os.Create(tmpPath)
				if err != nil {
					return false, fmt.Errorf("failed to create output file: %w", err)
				}

				if _, err := io.Copy(outFile, tarReader); err != nil {
					outFile.Close()
					return false, fmt.Errorf("failed to copy binary: %w", err)
				}

				// Checked, not deferred: a deferred Close discards the error,
				// and a failed flush would otherwise be reported as success.
				if err := outFile.Close(); err != nil {
					return false, fmt.Errorf("failed to flush binary: %w", err)
				}

				if err := os.Chmod(tmpPath, 0755); err != nil {
					return false, fmt.Errorf("failed to make binary executable: %w", err)
				}

				if err := os.Rename(tmpPath, outputFile); err != nil {
					return false, fmt.Errorf("failed to move binary into place: %w", err)
				}

				return true, nil
			}
		}
	}

	return false, nil
}
