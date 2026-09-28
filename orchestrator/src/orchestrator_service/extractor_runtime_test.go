package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestProcessReleaseStringTargetsRuntimeCodename pins that the rewrite targets
// the codename the process runs on, not a hard-coded one.
//
// The service image is ubuntu:jammy. Rewriting to "bullseye" produced a binary
// linked against libssl.so.1.1, libcrypto.so.1.1 and libffi.so.7, none of which
// exist on jammy, so every exec of it died with "error while loading shared
// libraries", exit 127 -- and the broken file was cached.
func TestProcessReleaseStringTargetsRuntimeCodename(t *testing.T) {
	for _, tc := range []struct {
		name     string
		release  string
		codename string
		want     string
	}{
		{"bullseye tag on a jammy host", "3.3.0-alpha1-compatible-90ff48c-bullseye-devnet", "jammy",
			"3.3.0-alpha1-compatible-90ff48c-jammy-devnet"},
		{"jammy tag on a bullseye host", "3.3.0-alpha1-compatible-90ff48c-jammy-devnet", "bullseye",
			"3.3.0-alpha1-compatible-90ff48c-bullseye-devnet"},
		{"already correct", "4.0.0-6965b50-jammy-devnet", "jammy", "4.0.0-6965b50-jammy-devnet"},
		{"no codename is left alone", "3.2.0-alpha1-app-state32-05da85d", "jammy",
			"3.2.0-alpha1-app-state32-05da85d"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := processReleaseString(tc.release, tc.codename); got != tc.want {
				t.Fatalf("processReleaseString(%q, %q) = %q, want %q",
					tc.release, tc.codename, got, tc.want)
			}
		})
	}

	// runtimeCodename must name a codename the rewrite understands, or every
	// tag would be rewritten to something that does not exist.
	if rc := runtimeCodename(); !osCodenames[rc] {
		t.Fatalf("runtimeCodename() = %q, which is not a known codename", rc)
	}
}

// TestExtractRejectsABinaryThatDoesNotRun: an extracted binary that cannot
// execute here must not be handed out or left in the cache.
func TestExtractRejectsABinaryThatDoesNotRun(t *testing.T) {
	f := &fakeRegistry{t: t, layers: [][]byte{
		gzippedLayer(t, "usr/local/bin/mina", "a-binary-for-the-wrong-distro"),
	}}
	r := f.start()
	r.verify = func(context.Context, string) error {
		return errors.New("extracted binary does not run here: exit status 127: " +
			"error while loading shared libraries: libssl.so.1.1")
	}

	out := filepath.Join(t.TempDir(), "mina")
	if err := r.extractMinaBinary(context.Background(), "4.0.0-rc1", out, testLog()); err != nil {
		t.Fatalf("extractMinaBinary: %v", err)
	}

	// extractMinaBinary itself writes the file; getMinaExecutablePath is what
	// verifies and evicts. Drive the verification the same way it does.
	if err := r.verifyBinary(context.Background(), out); err == nil {
		t.Fatal("a binary that cannot run was accepted")
	} else {
		if rerr := os.Remove(out); rerr != nil {
			t.Fatalf("removing the unusable binary: %v", rerr)
		}
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("an unusable binary was left at the cache path; every later run would serve it")
	}
}

// TestExtractFailsWhenAnUpperLayerCannotBeVerified is the regression test for
// the layer walk falling through on error.
//
// The walk runs top-down. When the mina layer failed to download or failed its
// digest check, `continue` carried on into lower layers and returned a copy the
// image had already replaced. In the real mina-daemon image the layer below
// mina is 9.3 GB, so a transient error also started a 9.3 GB download into the
// pod's /tmp.
func TestExtractFailsWhenAnUpperLayerCannotBeVerified(t *testing.T) {
	f := &fakeRegistry{t: t,
		layers: [][]byte{
			gzippedLayer(t, "usr/local/bin/mina", "STALE-3.3-the-image-already-replaced-this"),
			gzippedLayer(t, "usr/local/bin/mina", "CURRENT-4.0-the-one-the-manifest-names"),
		},
		// Only the TOP blob is served as content the digest does not name, so
		// the lower layer stays intact and readable. If the walk falls through
		// on error it will return STALE-3.3, which is the defect.
		corruptBlob:    gzippedLayer(t, "usr/local/bin/mina", "TAMPERED"),
		corruptOnlyTop: true,
	}
	r := f.start()

	out := filepath.Join(t.TempDir(), "mina")
	err := r.extractMinaBinary(context.Background(), "4.0.0-rc1", out, testLog())
	if err == nil {
		got, _ := os.ReadFile(out)
		t.Fatalf("a failed top layer was reported as a successful extraction, returning %q", got)
	}
	if _, statErr := os.Stat(out); statErr == nil {
		got, _ := os.ReadFile(out)
		t.Fatalf("a file was left at the cache path after a failed layer: %q", got)
	}
}
