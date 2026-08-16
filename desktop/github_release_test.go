package main

import (
	"runtime"
	"strings"
	"testing"
)

func TestTagFromReleaseLocation(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://github.com/nnlgsakib/membuss/releases/tag/v1.2.5", "v1.2.5"},
		{"https://github.com/nnlgsakib/membuss/releases/tag/v1.2.5?foo=1", "v1.2.5"},
		{"/nnlgsakib/membuss/releases/tag/v0.9.0", "v0.9.0"},
		{"https://example.com/nope", ""},
	}
	for _, c := range cases {
		got := tagFromReleaseLocation(c.in)
		if got != c.want {
			t.Errorf("tagFromReleaseLocation(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestExtractTagFromAtom(t *testing.T) {
	xml := `<?xml version="1.0"?>
<feed>
  <title>Releases</title>
  <entry>
    <title>v1.2.5</title>
    <link rel="alternate" type="text/html" href="https://github.com/nnlgsakib/membuss/releases/tag/v1.2.5"/>
  </entry>
</feed>`
	got := extractTagFromAtom(xml)
	if got != "v1.2.5" {
		t.Fatalf("extractTagFromAtom = %q want v1.2.5", got)
	}
}

func TestReleaseCache(t *testing.T) {
	clearReleaseCache()
	info := &latestReleaseInfo{TagName: "v9.9.9", Assets: map[string]string{}}
	cacheRelease(info)
	got, err := fetchLatestRelease()
	if err != nil {
		t.Fatalf("cached fetch failed: %v", err)
	}
	if got.TagName != "v9.9.9" {
		t.Fatalf("expected cached tag, got %s", got.TagName)
	}
	clearReleaseCache()
}

func TestPlatformArchiveAssetName(t *testing.T) {
	name := platformArchiveAssetName("v1.2.5")
	if !strings.HasPrefix(name, "membuss-v1.2.5-") {
		t.Fatalf("unexpected asset name prefix: %s", name)
	}
	if !strings.HasSuffix(name, ".zip") && !strings.HasSuffix(name, ".tar.gz") {
		t.Fatalf("unexpected asset extension: %s", name)
	}
}

func TestIsVersionNewer(t *testing.T) {
	if !isVersionNewer("1.2.0", "1.2.5") {
		t.Error("expected 1.2.5 > 1.2.0")
	}
	if isVersionNewer("1.2.5", "1.2.5") {
		t.Error("expected equal versions not newer")
	}
	if isVersionNewer("1.3.0", "1.2.5") {
		t.Error("expected 1.3.0 not older than 1.2.5")
	}
}

func TestFindPlatformAssetURL_Constructed(t *testing.T) {
	info := &latestReleaseInfo{TagName: "v1.2.5", Assets: map[string]string{}}
	url := findPlatformAssetURL(info)
	if url == "" {
		t.Fatal("expected constructed download URL")
	}
	if !strings.Contains(url, "/releases/download/v1.2.5/") {
		t.Fatalf("unexpected URL: %s", url)
	}
}

func TestParseReleaseTagOrURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://github.com/nnlgsakib/membuss/releases/tag/v2.8.4", "v2.8.4"},
		{"https://github.com/nnlgsakib/membuss/releases/tag/v2.8.3?ref=master", "v2.8.3"},
		{"https://github.com/nnlgsakib/membuss/releases/download/v2.8.4/membuss-v2.8.4-windows-amd64.zip", "v2.8.4"},
		{"v2.8.4", "v2.8.4"},
		{"2.8.4", "v2.8.4"},
		{"refs/tags/v2.8.0", "v2.8.0"},
		{"", ""},
	}
	for _, c := range cases {
		got := parseReleaseTagOrURL(c.in)
		if got != c.want {
			t.Errorf("parseReleaseTagOrURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFindDesktopAssetURL(t *testing.T) {
	info := &latestReleaseInfo{
		TagName: "v2.8.4",
		Assets: map[string]string{
			"membuss-desktop-v2.8.4-windows-amd64.zip": "https://custom.download/desktop.zip",
		},
	}
	url := findDesktopAssetURL(info)
	if url == "" {
		t.Fatal("expected desktop asset url")
	}
}

func TestFindDesktopInstallerURL(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("installer url check is Windows-specific")
	}
	info := &latestReleaseInfo{
		TagName: "v2.8.6",
		Assets: map[string]string{
			"Membuss-v2.8.6-windows-amd64-installer.exe": "https://github.com/nnlgsakib/membuss/releases/download/v2.8.6/Membuss-v2.8.6-windows-amd64-installer.exe",
		},
	}
	url := findDesktopInstallerURL(info)
	if url == "" {
		t.Fatal("expected installer url")
	}
}

func TestIsValidSemverTag(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"v2.8.4", true},
		{"2.8.4", true},
		{"v1.0.0-rc1", true},
		{"vgbdg", false},
		{"random_string", false},
		{"v", false},
		{"", false},
	}
	for _, c := range cases {
		got := isValidSemverTag(c.in)
		if got != c.want {
			t.Errorf("isValidSemverTag(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestFetchReleaseByTag_Invalid(t *testing.T) {
	_, err := fetchReleaseByTag("vgbdg")
	if err == nil {
		t.Fatal("expected error for invalid tag 'vgbdg', got nil")
	}
}

// TestFetchLatestRelease_Live hits GitHub; skipped if network fails so CI stays green.
func TestFetchLatestRelease_Live(t *testing.T) {
	info, err := fetchLatestRelease()
	if err != nil {
		t.Skipf("network/GitHub unavailable: %v", err)
	}
	if info.TagName == "" {
		t.Fatal("empty tag")
	}
	if !strings.HasPrefix(info.TagName, "v") && info.TagName[0] < '0' {
		t.Fatalf("suspicious tag: %s", info.TagName)
	}
}
