package main

import "testing"

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
			if got := processReleaseString(tt.release); got != tt.want {
				t.Errorf("processReleaseString(%q) = %q, want %q", tt.release, got, tt.want)
			}
		})
	}
}
