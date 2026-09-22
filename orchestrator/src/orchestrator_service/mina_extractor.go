package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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

// processReleaseString rewrites a release tag to its jammy build.
//
// Only a segment that actually names an OS codename is rewritten. Rewriting
// the second-to-last segment unconditionally assumed every tag ends in
// "<codename>-<network>", which most do not:
//
//	3.2.0-alpha1-app-state32-05da85d
//	  -> 3.2.0-alpha1-app-jammy-05da85d         (corrupted)
//	3.4.0-alpha1-mesa-mut-prefork-cac0e3e-jammy-mesa-mut-generic
//	  -> ...-jammy-mesa-jammy-generic           (corrupted)
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
			parts[i] = "jammy"
			return strings.Join(parts, "-")
		}
	}

	// No codename found -- the tag is not in a shape we understand, so leave
	// it alone rather than corrupting it.
	return release
}

// sanitiseRelease refuses a release tag that would escape the cache directory.
//
// The tag comes from deployment.metadata_json, a table this repo does not
// write, and it is joined onto cacheDir and later handed to exec.CommandContext
// as config.MinaExec. A value such as "../../../../usr/bin/env" would name a
// path outside the cache that the cache-hit branch then runs.
func sanitiseRelease(release string) error {
	if release == "" {
		return fmt.Errorf("empty release tag")
	}
	if strings.ContainsAny(release, `/\`) || release == ".." || strings.Contains(release, "..") {
		return fmt.Errorf("release tag %q contains a path separator", release)
	}
	return nil
}

// getMinaExecutablePath returns the path to the cached Mina executable,
// extracting it if necessary.
func getMinaExecutablePath(ctx context.Context, db *gorm.DB, log logging.StandardLogger) (string, error) {
	release, err := getLatestDeploymentRelease(db)
	if err != nil {
		return "", fmt.Errorf("failed to get deployment release: %w", err)
	}

	processedRelease := processReleaseString(release)
	if err := sanitiseRelease(processedRelease); err != nil {
		return "", fmt.Errorf("refusing deployment release: %w", err)
	}

	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create cache directory: %w", err)
	}

	executableName := fmt.Sprintf("mina-%s", processedRelease)
	executablePath := filepath.Join(cacheDir, executableName)

	if _, err := os.Stat(executablePath); err == nil {
		log.Infof("Using cached Mina executable: %s", executableName)
		return filepath.Abs(executablePath)
	}

	log.Infof("Extracting Mina executable from image %s:%s", defaultRegistry.repo, processedRelease)
	if err := defaultRegistry.extractMinaBinary(ctx, processedRelease, executablePath, log); err != nil {
		return "", fmt.Errorf("failed to extract mina binary: %w", err)
	}

	return filepath.Abs(executablePath)
}

// registry is the daemon image registry the mina client is taken from.
//
// The fields exist so that a test can point the extractor at an httptest
// server: every URL is built from baseURL, and accessToken is the ambient
// credential lookup.
type registry struct {
	baseURL     string
	service     string
	repo        string
	accessToken func(context.Context) (string, error)
}

var defaultRegistry = &registry{
	baseURL:     "https://europe-west3-docker.pkg.dev",
	service:     "europe-west3-docker.pkg.dev",
	repo:        "o1labs-192920/euro-docker-repo/mina-daemon",
	accessToken: ambientAccessToken,
}

// ambientAccessToken returns an OAuth access token for the identity this
// process runs as, or "" when there is none.
//
// Artifact Registry repositories are private, and Go's net/http attaches no
// credentials of its own, so Workload Identity alone does not authenticate a
// plain request: an anonymous /v2/token exchange answers
// 403 DENIED "Unauthenticated request". The token is read from the GCE
// metadata server, which is what Workload Identity populates; an operator can
// override it with REGISTRY_ACCESS_TOKEN, for example when running outside
// Google Cloud with `gcloud auth print-access-token`.
func ambientAccessToken(ctx context.Context) (string, error) {
	if tok := os.Getenv("REGISTRY_ACCESS_TOKEN"); tok != "" {
		return tok, nil
	}

	host := os.Getenv("GCE_METADATA_HOST")
	if host == "" {
		host = "metadata.google.internal"
	}
	url := fmt.Sprintf("http://%s/computeMetadata/v1/instance/service-accounts/default/token", host)

	// The metadata server is a link-local address that answers in
	// milliseconds or not at all, so this must not delay a run outside Google
	// Cloud.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Metadata-Flavor", "Google")

	resp, err := metadataClient.Do(req)
	if err != nil {
		// No metadata server: not an error, just no ambient identity.
		return "", nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil
	}

	var payload struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", nil
	}
	return payload.AccessToken, nil
}

// token performs the registry token exchange.
//
// With an ambient access token the exchange is authenticated with the
// oauth2accesstoken basic-auth user that Google Cloud registries expect;
// without one it is anonymous, which works only for a public repository.
func (r *registry) token(ctx context.Context, log logging.StandardLogger) (string, error) {
	tokenURL := fmt.Sprintf("%s/v2/token?service=%s&scope=repository:%s:pull",
		r.baseURL, r.service, r.repo)

	var accessToken string
	if r.accessToken != nil {
		var err error
		if accessToken, err = r.accessToken(ctx); err != nil {
			return "", fmt.Errorf("reading ambient credentials: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", tokenURL, nil)
	if err != nil {
		return "", err
	}
	if accessToken != "" {
		req.SetBasicAuth("oauth2accesstoken", accessToken)
		log.Debugf("Requesting a registry token with the ambient service-account identity")
	} else {
		log.Debugf("Requesting a registry token anonymously: no ambient credentials found")
	}

	resp, err := metadataClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if accessToken == "" && (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized) {
			return "", fmt.Errorf("token request failed with status %s and no ambient credentials were found; "+
				"the repository is private, so the pod needs a service account with "+
				"artifactregistry.repositories.downloadArtifacts, or REGISTRY_ACCESS_TOKEN must be set", resp.Status)
		}
		return "", fmt.Errorf("token request failed with status: %s", resp.Status)
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", err
	}

	// A registry that answers the exchange with no token still leaves the
	// ambient access token usable as a bearer against the v2 API.
	if tokenResp.Token == "" {
		if accessToken != "" {
			return accessToken, nil
		}
		return "", fmt.Errorf("empty token received")
	}

	return tokenResp.Token, nil
}

// manifestAccept lists every manifest media type the registry may answer with.
//
// Listing only manifest.v2 was not enough: anything pushed by
// `docker buildx build --push` is served as an OCI image index, whose JSON has
// a "manifests" array and no "layers", so the decode produced a manifest with
// zero layers and the extraction failed with "mina binary not found in any
// layer" for an image that carries it in every layer of its amd64 child.
const manifestAccept = "application/vnd.oci.image.index.v1+json, " +
	"application/vnd.docker.distribution.manifest.list.v2+json, " +
	"application/vnd.oci.image.manifest.v1+json, " +
	"application/vnd.docker.distribution.manifest.v2+json"

// indexEntry is one child of a manifest index.
type indexEntry struct {
	Digest   string `json:"digest"`
	Platform struct {
		Architecture string `json:"architecture"`
		OS           string `json:"os"`
	} `json:"platform"`
}

// manifest fetches the image manifest for a tag, resolving an index to its
// linux/amd64 child.
func (r *registry) manifest(ctx context.Context, token, reference string, log logging.StandardLogger) (*Manifest, error) {
	manifestURL := fmt.Sprintf("%s/v2/%s/manifests/%s", r.baseURL, r.repo, reference)

	log.Infof("Fetching image manifest: url=%s reference=%s", manifestURL, reference)

	req, err := http.NewRequestWithContext(ctx, "GET", manifestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", manifestAccept)

	resp, err := metadataClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manifest request failed with status: %s (url=%s, reference=%s)", resp.Status, manifestURL, reference)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading manifest: %w", err)
	}

	var doc struct {
		MediaType string       `json:"mediaType"`
		Layers    []Layer      `json:"layers"`
		Manifests []indexEntry `json:"manifests"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("decoding manifest: %w", err)
	}

	if len(doc.Layers) > 0 {
		return &Manifest{Layers: doc.Layers}, nil
	}

	if len(doc.Manifests) == 0 {
		return nil, fmt.Errorf("manifest for %s carries neither layers nor an index", reference)
	}

	for _, child := range doc.Manifests {
		if child.Platform.OS == "linux" && child.Platform.Architecture == "amd64" {
			log.Debugf("Manifest %s is an index; following its linux/amd64 child %s", reference, child.Digest)
			return r.manifest(ctx, token, child.Digest, log)
		}
	}
	return nil, fmt.Errorf("manifest index for %s has no linux/amd64 child", reference)
}

// extractMinaBinary extracts the mina binary from the daemon image.
func (r *registry) extractMinaBinary(ctx context.Context, tag, outputFile string, log logging.StandardLogger) error {
	log.Infof("Starting extraction of mina binary from %s:%s", r.repo, tag)

	tempDir, err := os.MkdirTemp("", "mina-extract-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer func() {
		os.RemoveAll(tempDir)
	}()

	log.Debugf("Getting registry token for Docker image extraction")
	token, err := r.token(ctx, log)
	if err != nil {
		return fmt.Errorf("failed to get registry token: %w", err)
	}
	log.Debugf("Registry token obtained successfully")

	log.Debugf("Getting image manifest for %s:%s", r.repo, tag)
	manifest, err := r.manifest(ctx, token, tag, log)
	if err != nil {
		return fmt.Errorf("failed to get image manifest: %w", err)
	}
	log.Infof("Image manifest obtained successfully, found %d layers", len(manifest.Layers))

	// Layers are searched from the top down. An image layer list runs base
	// first, and a later layer overrides an earlier one, so the first match
	// from the front can be a binary that a later `apt-get install
	// --allow-downgrades` has already replaced.
	for i := len(manifest.Layers) - 1; i >= 0; i-- {
		layer := manifest.Layers[i]
		log.Debugf("Processing layer %d/%d: %s", i+1, len(manifest.Layers), layer.Digest)
		found, deleted, err := r.processLayer(ctx, token, layer.Digest, tempDir, i+1, outputFile, log)
		if err != nil {
			log.Warnf("Failed to process layer %d: %v", i+1, err)
			continue
		}
		if deleted {
			// A whiteout entry records that the file was removed in this
			// layer, so any copy in a lower layer is not part of the image.
			return fmt.Errorf("the image deletes %s in layer %d", targetPath, i+1)
		}
		if found {
			// extractLayer chmods before renaming into place, so the file is
			// already executable by the time it exists at outputFile.
			log.Infof("Successfully extracted mina binary to: %s", outputFile)
			return nil
		}
	}

	return fmt.Errorf("mina binary not found in any layer")
}

func (r *registry) processLayer(ctx context.Context, token, digest, tempDir string, layerNum int, outputFile string, log logging.StandardLogger) (found, deleted bool, err error) {
	blobURL := fmt.Sprintf("%s/v2/%s/blobs/%s", r.baseURL, r.repo, digest)
	layerFile := filepath.Join(tempDir, fmt.Sprintf("layer_%d.tar.gz", layerNum))

	if err := downloadLayer(ctx, token, blobURL, digest, layerFile, log); err != nil {
		return false, false, fmt.Errorf("failed to download layer: %w", err)
	}
	defer os.Remove(layerFile)

	return extractLayerOrWhiteout(layerFile, outputFile, log)
}

// downloadLayer writes a blob to outputPath and proves it is the blob that was
// asked for.
//
// digest is the content address of the layer. Without checking it, a registry,
// proxy or CDN serving a stale, truncated or wrong blob produced a mina binary
// that was written, made executable and then run, and nothing noticed.
func downloadLayer(ctx context.Context, token, blobURL, digest, outputPath string, log logging.StandardLogger) error {
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

	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(outFile, hash), resp.Body); err != nil {
		outFile.Close()
		return err
	}
	// Checked rather than deferred: a failed flush here would otherwise be
	// reported as a complete layer and searched as if it were one.
	if err := outFile.Close(); err != nil {
		return err
	}

	if want := strings.TrimPrefix(digest, "sha256:"); want != "" && !strings.EqualFold(want, hex.EncodeToString(hash.Sum(nil))) {
		os.Remove(outputPath)
		return fmt.Errorf("layer %s does not match its digest: got sha256:%s", digest, hex.EncodeToString(hash.Sum(nil)))
	}
	return nil
}

// extractLayer reports whether the layer carries the mina binary, and writes
// it to outputFile when it does.
func extractLayer(layerFile, outputFile string, log logging.StandardLogger) (bool, error) {
	found, _, err := extractLayerOrWhiteout(layerFile, outputFile, log)
	return found, err
}

// extractLayerOrWhiteout also reports a whiteout entry, i.e. a record that the
// binary was deleted by this layer.
func extractLayerOrWhiteout(layerFile, outputFile string, log logging.StandardLogger) (found, deleted bool, err error) {
	file, err := os.Open(layerFile)
	if err != nil {
		return false, false, err
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
			return false, false, fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer gzReader.Close()
		reader = gzReader
	} else {
		// File is not gzipped, treat as plain tar
		file.Seek(0, 0)
	}

	tarReader := tar.NewReader(reader)
	whiteout := filepath.Dir(targetPath) + "/.wh." + filepath.Base(targetPath)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return false, false, fmt.Errorf("failed to read tar header: %w", err)
		}

		name := strings.TrimPrefix(header.Name, "./")
		if name == whiteout || strings.HasSuffix(name, "/"+whiteout) {
			log.Infof("Layer deletes %s (whiteout entry %s)", targetPath, header.Name)
			return false, true, nil
		}

		// Check if this is the mina binary we're looking for
		if name == targetPath || strings.HasSuffix(name, "/"+targetPath) {
			if header.Typeflag == tar.TypeReg {
				log.Infof("Found mina binary in layer at path: %s", header.Name)
				if err := writeBinary(tarReader, outputFile); err != nil {
					return false, false, err
				}
				return true, false, nil
			}
		}
	}

	return false, false, nil
}

// writeBinary copies the tar entry to outputFile through a temporary file in
// the same directory.
//
// Writing straight to outputFile meant a short read or a mid-copy failure left
// a partial file at the cache path; cache validity is a bare os.Stat, so every
// later run then reported "Using cached Mina executable" and pointed MinaExec
// at a truncated, non-executable file -- for good, until someone deleted it by
// hand. The temporary name comes from os.CreateTemp rather than the process
// id, so two extractions cannot share it.
func writeBinary(src io.Reader, outputFile string) error {
	outFile, err := os.CreateTemp(filepath.Dir(outputFile), filepath.Base(outputFile)+".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	tmpPath := outFile.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(outFile, src); err != nil {
		outFile.Close()
		return fmt.Errorf("failed to copy binary: %w", err)
	}

	// Checked, not deferred: a deferred Close discards the error, and a failed
	// flush would otherwise be reported as success.
	if err := outFile.Close(); err != nil {
		return fmt.Errorf("failed to flush binary: %w", err)
	}

	if err := os.Chmod(tmpPath, 0755); err != nil {
		return fmt.Errorf("failed to make binary executable: %w", err)
	}

	if err := os.Rename(tmpPath, outputFile); err != nil {
		return fmt.Errorf("failed to move binary into place: %w", err)
	}
	return nil
}
