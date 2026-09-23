package main

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	logging "github.com/ipfs/go-log/v2"
)

func TestProcessReleaseString(t *testing.T) {
	tests := []struct {
		name    string
		release string
		want    string
	}{
		{
			name:    "rewrites a non-jammy codename",
			release: "3.3.0-alpha1-compatible-90ff48c-bullseye-devnet",
			want:    "3.3.0-alpha1-compatible-90ff48c-jammy-devnet",
		},
		{
			name:    "leaves a jammy tag alone",
			release: "4.0.0-6965b50-jammy-devnet",
			want:    "4.0.0-6965b50-jammy-devnet",
		},
		{
			// The tag ITN2 ran from 2026-08-27. Rewriting the second-to-last
			// segment turned this into "…-jammy-mesa-jammy-generic", which does
			// not exist, and extraction 404'd on an image that was present.
			name:    "leaves a long suffix intact",
			release: "3.4.0-alpha1-mesa-mut-prefork-cac0e3e-jammy-mesa-mut-generic",
			want:    "3.4.0-alpha1-mesa-mut-prefork-cac0e3e-jammy-mesa-mut-generic",
		},
		{
			name:    "rewrites a codename ahead of a long suffix",
			release: "3.4.0-alpha1-mesa-mut-prefork-cac0e3e-noble-mesa-mut-generic",
			want:    "3.4.0-alpha1-mesa-mut-prefork-cac0e3e-jammy-mesa-mut-generic",
		},
		{
			name:    "leaves a tag with no codename alone",
			release: "3.2.0-alpha1-app-state32-05da85d",
			want:    "3.2.0-alpha1-app-state32-05da85d",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := processReleaseString(tt.release, "jammy"); got != tt.want {
				t.Errorf("processReleaseString(%q, \"jammy\") = %q, want %q", tt.release, got, tt.want)
			}
		})
	}
}

// tarWithShortEntry builds a tar whose single entry declares more bytes than it
// carries, which is what a truncated or corrupt layer looks like to io.Copy.
func tarWithShortEntry(t *testing.T, name string, declared int64, actual int) string {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name: name, Mode: 0644, Size: declared, Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	// Deliberately short, then abandon the writer without Flush/Close so the
	// archive really is truncated.
	if _, err := tw.Write(bytes.Repeat([]byte("x"), actual)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	path := filepath.Join(t.TempDir(), "layer.tar")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// TestExtractLayerLeavesNoPartialFile is the regression test for cache
// poisoning: a failed extraction used to leave a truncated, non-executable
// file at the cache path, and cache validity is a bare os.Stat, so every later
// run reported "Using cached Mina executable" and pointed MinaExec at it.
func TestExtractLayerLeavesNoPartialFile(t *testing.T) {
	const target = "usr/local/bin/mina"
	layer := tarWithShortEntry(t, target, 1000, 400)
	out := filepath.Join(t.TempDir(), "mina-3.3.0-jammy-devnet")

	found, err := extractLayer(layer, out, logging.Logger("extractor-test"))
	if err == nil {
		t.Fatalf("want an error for a truncated layer, got found=%v err=nil", found)
	}
	if found {
		t.Error("a failed extraction must not report the binary as found")
	}

	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		b, _ := os.ReadFile(out)
		t.Fatalf("a partial file was left at the cache path (%d bytes); "+
			"the next run would treat it as a valid cached executable", len(b))
	}

	matches, _ := filepath.Glob(out + ".tmp-*")
	if len(matches) != 0 {
		t.Errorf("temporary files left behind: %v", matches)
	}
}

// TestExtractLayerSuccessIsExecutable pins the success path: the binary lands
// at the cache path and is already executable, since the chmod now happens
// before the rename rather than in the caller.
func TestExtractLayerSuccessIsExecutable(t *testing.T) {
	const target = "usr/local/bin/mina"
	payload := []byte("#!/bin/sh\necho mina\n")

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name: "./" + target, Mode: 0644, Size: int64(len(payload)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	layer := filepath.Join(t.TempDir(), "layer.tar")
	if err := os.WriteFile(layer, buf.Bytes(), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	out := filepath.Join(t.TempDir(), "mina-3.3.0-jammy-devnet")
	found, err := extractLayer(layer, out, logging.Logger("extractor-test"))
	if err != nil || !found {
		t.Fatalf("extractLayer = (%v, %v), want (true, nil)", found, err)
	}

	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("binary missing from the cache path: %v", err)
	}
	if info.Mode().Perm()&0111 == 0 {
		t.Errorf("binary is not executable (mode %v); every exec.CommandContext would fail with EACCES", info.Mode())
	}
	if got, _ := os.ReadFile(out); !bytes.Equal(got, payload) {
		t.Errorf("binary contents = %q, want %q", got, payload)
	}
}
