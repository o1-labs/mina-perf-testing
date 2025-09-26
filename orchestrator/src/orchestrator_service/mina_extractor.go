package main

import (
	"archive/tar"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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

// processReleaseString processes the release string to ensure it uses bullseye
func processReleaseString(release string) string {
	// Split by dashes: e.g., "3.3.0-alpha1-compatible-90ff48c-bullseye-devnet"
	parts := strings.Split(release, "-")
	
	if len(parts) < 5 {
		// If format is unexpected, return as-is
		return release
	}
	
	// Check if the second-to-last part (index len-2) is not "bullseye"
	if parts[len(parts)-2] != "bullseye" {
		// Replace it with "bullseye"
		parts[len(parts)-2] = "bullseye"
	}
	
	return strings.Join(parts, "-")
}

// getMinaExecutablePath returns the path to the cached Mina executable, extracting it if necessary
func getMinaExecutablePath(db *gorm.DB, log logging.StandardLogger) (string, error) {
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
	dockerImage := fmt.Sprintf("gcr.io/o1labs-192920/mina-daemon:%s", processedRelease)
	log.Infof("Extracting Mina executable from Docker image: %s", dockerImage)
	
	if err := extractMinaBinary(dockerImage, executablePath, log); err != nil {
		return "", fmt.Errorf("failed to extract mina binary: %w", err)
	}
	
	return filepath.Abs(executablePath)
}

// extractMinaBinary extracts the mina binary from a Docker image
func extractMinaBinary(dockerImage, outputFile string, log logging.StandardLogger) error {
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
	token, err := getRegistryToken(log)
	if err != nil {
		return fmt.Errorf("failed to get registry token: %w", err)
	}
	log.Debugf("Registry token obtained successfully")

	// Get image manifest
	log.Debugf("Getting image manifest for %s", dockerImage)
	manifest, err := getImageManifest(token, dockerImage, log)
	if err != nil {
		return fmt.Errorf("failed to get image manifest: %w", err)
	}
	log.Debugf("Image manifest obtained successfully, found %d layers", len(manifest.Layers))

	// Download and search layers
	log.Infof("Searching for mina binary in %d layers", len(manifest.Layers))
	for i, layer := range manifest.Layers {
		log.Debugf("Processing layer %d/%d: %s", i+1, len(manifest.Layers), layer.Digest)
		if found, err := processLayer(token, layer.Digest, tempDir, i+1, outputFile, dockerImage, log); err != nil {
			log.Warnf("Failed to process layer %d: %v", i+1, err)
			continue
		} else if found {
			// Make executable
			if err := os.Chmod(outputFile, 0755); err != nil {
				return fmt.Errorf("failed to make binary executable: %w", err)
			}
			
			log.Infof("Successfully extracted mina binary to: %s", outputFile)
			return nil
		}
	}

	return fmt.Errorf("mina binary not found in any layer")
}

func getRegistryToken(log logging.StandardLogger) (string, error) {
	tokenURL := "https://gcr.io/v2/token?service=gcr.io&scope=repository:o1labs-192920/mina-daemon:pull"
	
	resp, err := http.Get(tokenURL)
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

func getImageManifest(token, dockerImage string, log logging.StandardLogger) (*Manifest, error) {
	// Parse the docker image to extract repository and tag
	parts := strings.Split(dockerImage, ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid docker image format: %s", dockerImage)
	}
	
	repository := parts[0]
	tag := parts[1]
	
	// Remove registry prefix for the API call
	repo := strings.TrimPrefix(repository, "gcr.io/")
	
	manifestURL := fmt.Sprintf("https://gcr.io/v2/%s/manifests/%s", repo, tag)
	
	req, err := http.NewRequest("GET", manifestURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manifest request failed with status: %s", resp.Status)
	}

	var manifest Manifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return nil, err
	}

	return &manifest, nil
}

func processLayer(token, digest, tempDir string, layerNum int, outputFile, dockerImage string, log logging.StandardLogger) (bool, error) {
	// Parse repository from dockerImage for blob URL
	parts := strings.Split(dockerImage, ":")
	repository := parts[0]
	repo := strings.TrimPrefix(repository, "gcr.io/")
	
	// Download layer
	blobURL := fmt.Sprintf("https://gcr.io/v2/%s/blobs/%s", repo, digest)
	layerFile := filepath.Join(tempDir, fmt.Sprintf("layer_%d.tar.gz", layerNum))

	if err := downloadLayer(token, blobURL, layerFile, log); err != nil {
		return false, fmt.Errorf("failed to download layer: %w", err)
	}
	defer os.Remove(layerFile)

	// Extract layer and search for mina binary
	return extractLayer(layerFile, outputFile, log)
}

func downloadLayer(token, blobURL, outputPath string, log logging.StandardLogger) error {
	log.Debugf("Downloading layer from: %s", blobURL)
	req, err := http.NewRequest("GET", blobURL, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
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
	defer outFile.Close()

	_, err = io.Copy(outFile, resp.Body)
	return err
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
				
				outFile, err := os.Create(outputFile)
				if err != nil {
					return false, fmt.Errorf("failed to create output file: %w", err)
				}
				defer outFile.Close()

				_, err = io.Copy(outFile, tarReader)
				if err != nil {
					return false, fmt.Errorf("failed to copy binary: %w", err)
				}

				return true, nil
			}
		}
	}

	return false, nil
}
