package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestReleaseCommit pins which segment of a release tag is the build commit.
// The gate compares commits, so reading the wrong segment would compare the
// wrong thing.
func TestReleaseCommit(t *testing.T) {
	for _, tc := range []struct{ release, want string }{
		{"4.0.0-rc1-83b4654", "83b4654"},
		{"4.0.0-6965b50", "6965b50"},
		{"3.3.0-alpha1-compatible-90ff48c-jammy-devnet", "90ff48c"},
		{"3.4.0-alpha1-mesa-mut-prefork-cac0e3e-jammy-mesa-mut-generic", "cac0e3e"},
		// "devnet" and "generic" are not hex; "bullseye" is a codename and is
		// skipped even though it is not hex either.
		{"3.2.0-alpha1-app-state32-05da85d", "05da85d"},
		{"no-commit-here", ""},
		{"", ""},
	} {
		if got := releaseCommit(tc.release); got != tc.want {
			t.Errorf("releaseCommit(%q) = %q, want %q", tc.release, got, tc.want)
		}
	}
}

// TestCommitsMatch: the tag carries an abbreviated commit, the client reports
// the full one.
func TestCommitsMatch(t *testing.T) {
	const full = "83b46545f5e0a1b2c3d4e5f60718293a4b5c6d7e"
	if !commitsMatch("83b4654", full) {
		t.Error("an abbreviated tag commit must match the full client commit it prefixes")
	}
	if commitsMatch("83b4655", full) {
		t.Error("a different commit must not match")
	}
	if commitsMatch("", full) || commitsMatch("83b4654", "") {
		t.Error("an unknown commit matches nothing")
	}
}

// fakeMinaClient writes a script that answers --version the way the real
// client does.
func fakeMinaClient(t *testing.T, commit string, exitCode int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake client is a shell script")
	}
	path := filepath.Join(t.TempDir(), "mina")
	body := "#!/bin/sh\n"
	if commit != "" {
		body += "echo \"Mina 4.0.0-rc1 (commit " + commit + ")\"\n"
	}
	body += "exit " + string(rune('0'+exitCode)) + "\n"
	if err := os.WriteFile(path, []byte(body), 0755); err != nil {
		t.Fatalf("writing the fake client: %v", err)
	}
	return path
}

// TestMinaCommitReadsTheBinary covers the three answers the gate acts on: a
// commit, no commit in the output, and a client that will not run.
func TestMinaCommitReadsTheBinary(t *testing.T) {
	const commit = "83b46545f5e0a1b2c3d4e5f60718293a4b5c6d7e"

	got, err := minaCommit(fakeMinaClient(t, commit, 0))
	if err != nil {
		t.Fatalf("minaCommit: %v", err)
	}
	if got != commit {
		t.Fatalf("commit = %q, want %q", got, commit)
	}

	if _, err := minaCommit(fakeMinaClient(t, "", 0)); err == nil {
		t.Error("a client whose output carries no commit must be an error")
	}
	if _, err := minaCommit(fakeMinaClient(t, commit, 1)); err == nil {
		t.Error("a client that exits non-zero must be an error")
	}
	if _, err := minaCommit(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Error("a client that does not exist must be an error")
	}
}

// TestDecideMinaClient is the table the gate is meant to implement:
// (extraction ok or failed) x (commit match, mismatch, unknown).
//
// The shape that mattered is the first two rows. Before this, the decision was
// made on the extraction alone, so it refused a run whose configured client is
// exactly the build the deployment names -- the `4.0.0-rc1-83b4654` case,
// where the tag carries no OS codename, the manifest 404s and the bundled
// client is the same build -- and it allowed a run whose extracted binary
// could not be executed at all.
func TestDecideMinaClient(t *testing.T) {
	const (
		fullCommit  = "83b46545f5e0a1b2c3d4e5f60718293a4b5c6d7e"
		otherCommit = "aaaa111bbbb222ccc333ddd444eee555fff66677"
		release     = "4.0.0-rc1-83b4654"
	)
	extractFailed := errors.New("manifest request failed with status: 404 Not Found")
	wontRun := errors.New("fork/exec: permission denied")

	for _, tc := range []struct {
		name     string
		evidence clientEvidence
		wantExec string
		wantWarn bool
		wantErr  string
	}{
		{
			name: "extraction ok and the binary runs",
			evidence: clientEvidence{
				extractedPath: "/cache/mina-4.0.0", extractedCommit: fullCommit,
				release: release, configuredExec: "/usr/local/bin/mina",
			},
			wantExec: "/cache/mina-4.0.0",
		},
		{
			name: "extraction ok but the binary will not run",
			evidence: clientEvidence{
				extractedPath: "/cache/mina-4.0.0", extractedCommitErr: wontRun,
				release: release, configuredExec: "/usr/local/bin/mina",
			},
			wantErr: "does not report a version",
		},
		{
			name: "extraction failed and the configured client matches by commit",
			evidence: clientEvidence{
				extractErr: extractFailed, release: release,
				configuredExec: "/usr/local/bin/mina", configuredCommit: fullCommit,
			},
			wantExec: "/usr/local/bin/mina",
			wantWarn: true,
		},
		{
			name: "extraction failed and the configured client is a different build",
			evidence: clientEvidence{
				extractErr: extractFailed, release: release,
				configuredExec: "/usr/local/bin/mina", configuredCommit: otherCommit,
			},
			wantErr: "Refusing to run",
		},
		{
			name: "extraction failed and the configured client will not run",
			evidence: clientEvidence{
				extractErr: extractFailed, release: release,
				configuredExec: "/usr/local/bin/mina", configuredCommitErr: wontRun,
			},
			wantErr: "Refusing to run",
		},
		{
			name: "extraction failed and the release carries no commit",
			evidence: clientEvidence{
				extractErr: extractFailed, release: "nightly",
				configuredExec: "/usr/local/bin/mina", configuredCommit: fullCommit,
			},
			wantErr: "Refusing to run",
		},
		{
			name: "extraction failed and the release cannot be read",
			evidence: clientEvidence{
				extractErr: extractFailed, releaseErr: errors.New("no release found in deployment metadata"),
				configuredExec: "/usr/local/bin/mina", configuredCommit: fullCommit,
			},
			wantErr: "Refusing to run",
		},
		{
			name: "a mismatch the operator has accepted",
			evidence: clientEvidence{
				extractErr: extractFailed, release: release,
				configuredExec: "/usr/local/bin/mina", configuredCommit: otherCommit,
				allowUnverified: true,
			},
			wantExec: "/usr/local/bin/mina",
			wantWarn: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			execPath, warning, err := decideMinaClient(tc.evidence)

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("want a refusal naming %q, got exec=%q warning=%q", tc.wantErr, execPath, warning)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to name %q", err, tc.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("want the run to proceed, got %v", err)
			}
			if execPath != tc.wantExec {
				t.Errorf("exec = %q, want %q", execPath, tc.wantExec)
			}
			if tc.wantWarn && warning == "" {
				t.Error("want a warning explaining why the pairing was accepted unproven")
			}
			if !tc.wantWarn && warning != "" {
				t.Errorf("unexpected warning: %s", warning)
			}
		})
	}
}
