package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	logging "github.com/ipfs/go-log/v2"
)

func testLog() logging.StandardLogger { return logging.Logger("extractor-test") }

// gzippedLayer builds a one-entry gzipped tar carrying contents at name.
func gzippedLayer(t *testing.T, name, contents string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: name, Mode: 0755, Size: int64(len(contents)), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write([]byte(contents)); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// fakeRegistry serves the three calls the extractor makes.
type fakeRegistry struct {
	t *testing.T
	// layers are served in manifest order, i.e. base first.
	layers [][]byte
	// index, when true, answers the tag with an OCI image index whose
	// linux/amd64 child carries the layers.
	index bool
	// tokenStatus, when non-zero, is the status the token exchange answers.
	tokenStatus int
	// corruptBlob serves a well-formed layer that is not the one the digest
	// names -- the shape a stale cache or a tampering proxy produces, and the
	// one that is invisible without a digest check.
	corruptBlob []byte

	sawBasicUser string
	srv          *httptest.Server
}

func (f *fakeRegistry) start() *registry {
	mux := http.NewServeMux()

	mux.HandleFunc("/v2/token", func(w http.ResponseWriter, r *http.Request) {
		if user, _, ok := r.BasicAuth(); ok {
			f.sawBasicUser = user
		}
		if f.tokenStatus != 0 {
			w.WriteHeader(f.tokenStatus)
			fmt.Fprint(w, `{"errors":[{"code":"DENIED","message":"Unauthenticated request."}]}`)
			return
		}
		fmt.Fprint(w, `{"token":"registry-token"}`)
	})

	byDigest := map[string][]byte{}
	for _, layer := range f.layers {
		byDigest[digestOf(layer)] = layer
	}

	manifestBody := func() []byte {
		entries := make([]map[string]any, 0, len(f.layers))
		for _, layer := range f.layers {
			entries = append(entries, map[string]any{
				"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip",
				"digest":    digestOf(layer),
				"size":      len(layer),
			})
		}
		body, _ := json.Marshal(map[string]any{
			"schemaVersion": 2,
			"mediaType":     "application/vnd.oci.image.manifest.v1+json",
			"layers":        entries,
		})
		return body
	}()

	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/manifests/"):
			if r.Header.Get("Authorization") != "Bearer registry-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			reference := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			if f.index && reference != "sha256:amd64child" {
				body, _ := json.Marshal(map[string]any{
					"schemaVersion": 2,
					"mediaType":     "application/vnd.oci.image.index.v1+json",
					"manifests": []map[string]any{
						{"digest": "sha256:arm64child", "platform": map[string]string{"os": "linux", "architecture": "arm64"}},
						{"digest": "sha256:amd64child", "platform": map[string]string{"os": "linux", "architecture": "amd64"}},
					},
				})
				w.Write(body)
				return
			}
			w.Write(manifestBody)
		case strings.Contains(r.URL.Path, "/blobs/"):
			digest := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			layer, ok := byDigest[digest]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if f.corruptBlob != nil {
				w.Write(f.corruptBlob)
				return
			}
			w.Write(layer)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	f.srv = httptest.NewServer(mux)
	f.t.Cleanup(f.srv.Close)

	return &registry{
		baseURL:     f.srv.URL,
		service:     "registry.test",
		repo:        "o1labs/mina-daemon",
		accessToken: func(context.Context) (string, error) { return "", nil },
	}
}

// TestExtractTakesTheTopmostLayer pins the search order. An image layer list
// runs base first and a later layer overrides an earlier one, so taking the
// first match from the front extracted a binary that a later
// `apt-get install --allow-downgrades` had already replaced.
func TestExtractTakesTheTopmostLayer(t *testing.T) {
	f := &fakeRegistry{t: t, layers: [][]byte{
		gzippedLayer(t, "usr/local/bin/mina", "OLD-base-layer-binary"),
		gzippedLayer(t, "usr/local/bin/mina", "NEW-top-layer-binary"),
	}}
	r := f.start()

	out := filepath.Join(t.TempDir(), "mina")
	if err := r.extractMinaBinary(context.Background(), "4.0.0-rc1", out, testLog()); err != nil {
		t.Fatalf("extractMinaBinary: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading extracted binary: %v", err)
	}
	if string(got) != "NEW-top-layer-binary" {
		t.Fatalf("extracted %q, want the top layer's binary", got)
	}
}

// TestExtractHonoursAWhiteout: a layer that deletes the binary means the image
// does not carry it, whatever a lower layer holds.
func TestExtractHonoursAWhiteout(t *testing.T) {
	f := &fakeRegistry{t: t, layers: [][]byte{
		gzippedLayer(t, "usr/local/bin/mina", "base-layer-binary"),
		gzippedLayer(t, "usr/local/bin/.wh.mina", ""),
	}}
	r := f.start()

	out := filepath.Join(t.TempDir(), "mina")
	err := r.extractMinaBinary(context.Background(), "4.0.0-rc1", out, testLog())
	if err == nil {
		t.Fatal("a deleted binary was reported as extracted")
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Fatal("a file was left at the cache path although the image deletes it")
	}
}

// TestExtractFollowsAnOCIIndex: anything pushed by `docker buildx build
// --push` is served as an index, whose JSON has no "layers" at all.
func TestExtractFollowsAnOCIIndex(t *testing.T) {
	f := &fakeRegistry{t: t, index: true, layers: [][]byte{
		gzippedLayer(t, "usr/local/bin/mina", "amd64-binary"),
	}}
	r := f.start()

	out := filepath.Join(t.TempDir(), "mina")
	if err := r.extractMinaBinary(context.Background(), "4.0.0-rc1", out, testLog()); err != nil {
		t.Fatalf("extractMinaBinary against an index: %v", err)
	}
	got, _ := os.ReadFile(out)
	if string(got) != "amd64-binary" {
		t.Fatalf("extracted %q, want the linux/amd64 child's binary", got)
	}
}

// TestDownloadLayerRejectsAWrongBlob: the digest is the only proof that the
// bytes are the layer that was asked for.
func TestDownloadLayerRejectsAWrongBlob(t *testing.T) {
	f := &fakeRegistry{
		t:           t,
		layers:      [][]byte{gzippedLayer(t, "usr/local/bin/mina", "the-binary-the-manifest-names")},
		corruptBlob: gzippedLayer(t, "usr/local/bin/mina", "SUBSTITUTED-binary"),
	}
	r := f.start()

	out := filepath.Join(t.TempDir(), "mina")
	err := r.extractMinaBinary(context.Background(), "4.0.0-rc1", out, testLog())
	if err == nil {
		t.Fatal("a blob that does not match its digest was accepted")
	}
	if _, statErr := os.Stat(out); statErr == nil {
		got, _ := os.ReadFile(out)
		t.Fatalf("a binary was written from a substituted blob: %q", got)
	}
}

// TestTokenUsesAmbientCredentials: the repository is private, so an anonymous
// exchange is refused. The pod's own identity is what authenticates it.
func TestTokenUsesAmbientCredentials(t *testing.T) {
	f := &fakeRegistry{t: t, layers: [][]byte{gzippedLayer(t, "usr/local/bin/mina", "binary")}}
	r := f.start()
	r.accessToken = func(context.Context) (string, error) { return "ya29.ambient-token", nil }

	if _, err := r.token(context.Background(), testLog()); err != nil {
		t.Fatalf("token: %v", err)
	}
	if f.sawBasicUser != "oauth2accesstoken" {
		t.Fatalf("registry saw basic-auth user %q, want oauth2accesstoken", f.sawBasicUser)
	}
}

// TestTokenRefusalNamesTheCause: a 403 with no ambient identity is the shape
// an unauthenticated pod sees, and the message must say what to fix.
func TestTokenRefusalNamesTheCause(t *testing.T) {
	f := &fakeRegistry{t: t, tokenStatus: http.StatusForbidden}
	r := f.start()

	_, err := r.token(context.Background(), testLog())
	if err == nil {
		t.Fatal("a 403 token exchange was reported as success")
	}
	if !strings.Contains(err.Error(), "REGISTRY_ACCESS_TOKEN") {
		t.Fatalf("err = %v, want it to name the credential it needs", err)
	}
}

// TestAmbientAccessTokenReadsTheMetadataServer pins the Workload Identity
// path, with the metadata host pointed at a test server.
func TestAmbientAccessTokenReadsTheMetadataServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Metadata-Flavor") != "Google" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		fmt.Fprint(w, `{"access_token":"ya29.metadata-token","expires_in":3599}`)
	}))
	defer srv.Close()

	t.Setenv("REGISTRY_ACCESS_TOKEN", "")
	t.Setenv("GCE_METADATA_HOST", strings.TrimPrefix(srv.URL, "http://"))

	tok, err := ambientAccessToken(context.Background())
	if err != nil {
		t.Fatalf("ambientAccessToken: %v", err)
	}
	if tok != "ya29.metadata-token" {
		t.Fatalf("token = %q, want the metadata server's answer", tok)
	}
}

// TestSanitiseRelease: the tag comes from a table this repo does not write,
// and it ends up both as a cache path and as the binary that is executed.
func TestSanitiseRelease(t *testing.T) {
	for _, tc := range []struct {
		release string
		wantErr bool
	}{
		{"4.0.0-rc1-83b4654", false},
		{"3.3.0-alpha1-compatible-90ff48c-bullseye-devnet", false},
		{"../../../../usr/bin/env", true},
		{`..\..\windows`, true},
		{"", true},
	} {
		err := sanitiseRelease(tc.release)
		if tc.wantErr && err == nil {
			t.Errorf("sanitiseRelease(%q) accepted a path-escaping tag", tc.release)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("sanitiseRelease(%q) = %v, want accepted", tc.release, err)
		}
	}
}
