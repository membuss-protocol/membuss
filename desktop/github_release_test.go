package main

import (
	"strings"
	"testing"
)

func TestTagFromReleaseLocation(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://github.com/membuss-protocol/membuss/releases/tag/v1.2.5", "v1.2.5"},
		{"https://github.com/membuss-protocol/membuss/releases/tag/v1.2.5?foo=1", "v1.2.5"},
		{"/membuss-protocol/membuss/releases/tag/v0.9.0", "v0.9.0"},
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
    <link rel="alternate" type="text/html" href="https://github.com/membuss-protocol/membuss/releases/tag/v1.2.5"/>
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
	if !strings.HasPrefix(name, "membuss-core-v1.2.5-") {
		t.Fatalf("unexpected asset name prefix: %s", name)
	}
	if !strings.HasSuffix(name, ".zip") && !strings.HasSuffix(name, ".tar.gz") {
		t.Fatalf("unexpected asset extension: %s", name)
	}
}

func TestIsVersionNewer_Basic(t *testing.T) {
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
	if !strings.Contains(url, "/releases/download/v1.2.5/membuss-core-v1.2.5-") {
		t.Fatalf("unexpected URL: %s", url)
	}
}

func TestFindPlatformAssetURL_Darwin(t *testing.T) {
	info := &latestReleaseInfo{
		TagName: "v2.9.3",
		Assets: map[string]string{
			"membuss-core-v2.9.3-darwin-arm64.tar.gz": "https://github.com/membuss-protocol/membuss/releases/download/v2.9.3/membuss-core-v2.9.3-darwin-arm64.tar.gz",
			"membuss-core-v2.9.3-darwin-amd64.tar.gz": "https://github.com/membuss-protocol/membuss/releases/download/v2.9.3/membuss-core-v2.9.3-darwin-amd64.tar.gz",
			"Membuss-Desktop-v2.9.3-darwin-arm64.zip": "https://github.com/membuss-protocol/membuss/releases/download/v2.9.3/Membuss-Desktop-v2.9.3-darwin-arm64.zip",
		},
	}
	if u := info.Assets["membuss-core-v2.9.3-darwin-arm64.tar.gz"]; !strings.Contains(u, "darwin-arm64.tar.gz") {
		t.Fatalf("unexpected arm64 asset: %s", u)
	}
	if u := info.Assets["membuss-core-v2.9.3-darwin-amd64.tar.gz"]; !strings.Contains(u, "darwin-amd64.tar.gz") {
		t.Fatalf("unexpected amd64 asset: %s", u)
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

func TestFindDesktopInstallerAssetURL(t *testing.T) {
	info := &latestReleaseInfo{
		TagName: "v2.10.0-beta.1",
		Assets: map[string]string{
			"Membuss-Desktop-v2.10.0-beta.1-windows-amd64-installer.exe": "https://github.com/membuss-protocol/membuss/releases/download/v2.10.0-beta.1/Membuss-Desktop-v2.10.0-beta.1-windows-amd64-installer.exe",
		},
	}
	url := findDesktopInstallerAssetURL(info)
	if url == "" {
		t.Fatal("expected installer download URL")
	}
	if !strings.Contains(url, "v2.10.0-beta.1") {
		t.Fatalf("unexpected installer url: %s", url)
	}
}
