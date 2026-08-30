package main

import (
	"testing"
)

func TestParseSemVer(t *testing.T) {
	tests := []struct {
		input string
		want  SemVer
	}{
		{"2.9.4", SemVer{Major: 2, Minor: 9, Patch: 4, Prerelease: ""}},
		{"v2.10.0-beta.1", SemVer{Major: 2, Minor: 10, Patch: 0, Prerelease: "beta.1"}},
		{"v1.0.0-rc.3", SemVer{Major: 1, Minor: 0, Patch: 0, Prerelease: "rc.3"}},
		{"0.1.0-alpha", SemVer{Major: 0, Minor: 1, Patch: 0, Prerelease: "alpha"}},
		{"", SemVer{}},
	}

	for _, tc := range tests {
		got := ParseSemVer(tc.input)
		if got != tc.want {
			t.Errorf("ParseSemVer(%q) = %+v, want %+v", tc.input, got, tc.want)
		}
	}
}

func TestCompareSemVer(t *testing.T) {
	tests := []struct {
		v1   string
		v2   string
		want int
	}{
		// Basic patch/minor/major comparisons
		{"1.2.0", "1.2.5", -1},
		{"1.2.5", "1.2.5", 0},
		{"1.3.0", "1.2.5", 1},
		{"2.0.0", "1.99.99", 1},
		{"v2.9.4", "v2.10.0", -1},

		// Pre-release vs Stable
		{"2.10.0-beta.1", "2.10.0", -1}, // pre-release < stable of same version
		{"2.10.0", "2.10.0-beta.1", 1},  // stable > pre-release of same version
		{"2.9.4", "2.10.0-beta.1", -1},  // older stable < newer beta
		{"2.10.0-beta.1", "2.9.4", 1},   // newer beta > older stable

		// Pre-release sequence
		{"2.10.0-alpha.1", "2.10.0-beta.1", -1},
		{"2.10.0-beta.1", "2.10.0-beta.2", -1},
		{"2.10.0-beta.2", "2.10.0-rc.1", -1},
		{"2.10.0-beta.1", "2.10.0-beta.1", 0},
	}

	for _, tc := range tests {
		got := CompareSemVer(tc.v1, tc.v2)
		if got != tc.want {
			t.Errorf("CompareSemVer(%q, %q) = %d, want %d", tc.v1, tc.v2, got, tc.want)
		}
	}
}

func TestIsVersionNewer(t *testing.T) {
	if !IsVersionNewer("2.9.4", "2.10.0-beta.1") {
		t.Errorf("expected 2.10.0-beta.1 to be newer than 2.9.4")
	}
	if !IsVersionNewer("2.10.0-beta.1", "2.10.0-beta.2") {
		t.Errorf("expected 2.10.0-beta.2 to be newer than 2.10.0-beta.1")
	}
	if !IsVersionNewer("2.10.0-beta.1", "2.10.0") {
		t.Errorf("expected 2.10.0 to be newer than 2.10.0-beta.1")
	}
	if IsVersionNewer("2.10.0-beta.1", "2.9.4") {
		t.Errorf("expected 2.9.4 to NOT be newer than 2.10.0-beta.1")
	}
}

func TestIsPrerelease(t *testing.T) {
	if !IsPrerelease("v2.10.0-beta.1") {
		t.Errorf("expected v2.10.0-beta.1 to be prerelease")
	}
	if !IsPrerelease("2.0.0-rc.2") {
		t.Errorf("expected 2.0.0-rc.2 to be prerelease")
	}
	if IsPrerelease("v2.9.4") {
		t.Errorf("expected v2.9.4 to NOT be prerelease")
	}
}
