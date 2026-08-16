package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	githubRepoOwner  = "nnlgsakib"
	githubRepoName   = "membuss"
	githubAPILatest  = "https://api.github.com/repos/" + githubRepoOwner + "/" + githubRepoName + "/releases/latest"
	githubHTMLLatest = "https://github.com/" + githubRepoOwner + "/" + githubRepoName + "/releases/latest"
	githubAtomFeed   = "https://github.com/" + githubRepoOwner + "/" + githubRepoName + "/releases.atom"
	// Cache avoids hammering GitHub when the user clicks Check Updates repeatedly.
	releaseCacheTTL = 30 * time.Minute
)

func desktopUserAgent() string {
	return "Membuss-Desktop/" + runtime.GOOS + "-" + runtime.GOARCH
}

// latestReleaseInfo is the subset of GitHub release data we need.
type latestReleaseInfo struct {
	TagName string
	// Assets maps asset file name → browser_download_url (may be empty when
	// we only resolved the tag via non-API fallbacks).
	Assets map[string]string
	// FromAPI is true when the full JSON API response was used.
	FromAPI bool
}

var (
	releaseCacheMu   sync.Mutex
	releaseCacheInfo *latestReleaseInfo
	releaseCacheAt   time.Time
)

// fetchLatestRelease resolves the latest release tag without depending on the
// rate-limited GitHub REST API.
//
// Order (rate-limit safe first):
//  1. In-memory cache (30m)
//  2. HTML /releases/latest redirect (no API quota)
//  3. Atom feed /releases.atom (no API quota)
//  4. REST API last (optional; only if non-API paths fail; uses GITHUB_TOKEN if set)
func fetchLatestRelease() (*latestReleaseInfo, error) {
	releaseCacheMu.Lock()
	if releaseCacheInfo != nil && time.Since(releaseCacheAt) < releaseCacheTTL && releaseCacheInfo.TagName != "" {
		cp := *releaseCacheInfo
		if releaseCacheInfo.Assets != nil {
			cp.Assets = make(map[string]string, len(releaseCacheInfo.Assets))
			for k, v := range releaseCacheInfo.Assets {
				cp.Assets[k] = v
			}
		}
		releaseCacheMu.Unlock()
		return &cp, nil
	}
	releaseCacheMu.Unlock()

	var errs []string

	// 1) HTML redirect — primary path, no api.github.com quota.
	if info, err := fetchLatestReleaseRedirect(); err == nil && info != nil && info.TagName != "" {
		return cacheRelease(info), nil
	} else if err != nil {
		errs = append(errs, "redirect: "+err.Error())
	}

	// 2) Atom feed — also outside the REST API budget.
	if info, err := fetchLatestReleaseAtom(); err == nil && info != nil && info.TagName != "" {
		return cacheRelease(info), nil
	} else if err != nil {
		errs = append(errs, "atom: "+err.Error())
	}

	// 3) REST API last resort (may 403 when rate-limited).
	if info, err := fetchLatestReleaseAPI(); err == nil && info != nil && info.TagName != "" {
		return cacheRelease(info), nil
	} else if err != nil {
		errs = append(errs, "api: "+err.Error())
	}

	if len(errs) == 0 {
		return nil, fmt.Errorf("github release lookup returned empty tag")
	}
	return nil, fmt.Errorf("github release lookup failed (%s)", strings.Join(errs, "; "))
}

func cacheRelease(info *latestReleaseInfo) *latestReleaseInfo {
	releaseCacheMu.Lock()
	defer releaseCacheMu.Unlock()
	releaseCacheInfo = info
	releaseCacheAt = time.Now()
	return info
}

// clearReleaseCache is used by tests.
func clearReleaseCache() {
	releaseCacheMu.Lock()
	defer releaseCacheMu.Unlock()
	releaseCacheInfo = nil
	releaseCacheAt = time.Time{}
}

func newGitHubHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

func applyGitHubAPIHeaders(req *http.Request) {
	req.Header.Set("User-Agent", desktopUserAgent())
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if tok := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	} else if tok := strings.TrimSpace(os.Getenv("GH_TOKEN")); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
}

func applyBrowserHeaders(req *http.Request) {
	req.Header.Set("User-Agent", desktopUserAgent())
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
}

func fetchLatestReleaseAPI() (*latestReleaseInfo, error) {
	req, err := http.NewRequest(http.MethodGet, githubAPILatest, nil)
	if err != nil {
		return nil, err
	}
	applyGitHubAPIHeaders(req)

	resp, err := newGitHubHTTPClient(15 * time.Second).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return nil, fmt.Errorf("API rate limit exceeded (%s)", abbreviate(msg, 160))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned %s: %s", resp.Status, abbreviate(string(body), 160))
	}

	var release map[string]any
	if err := json.Unmarshal(body, &release); err != nil {
		return nil, fmt.Errorf("decode release JSON: %w", err)
	}
	tag, _ := release["tag_name"].(string)
	if tag == "" {
		return nil, fmt.Errorf("release JSON missing tag_name")
	}

	info := &latestReleaseInfo{
		TagName: tag,
		Assets:  make(map[string]string),
		FromAPI: true,
	}
	if assets, ok := release["assets"].([]any); ok {
		for _, a := range assets {
			m, ok := a.(map[string]any)
			if !ok {
				continue
			}
			name, _ := m["name"].(string)
			url, _ := m["browser_download_url"].(string)
			if name != "" && url != "" {
				info.Assets[name] = url
			}
		}
	}
	return info, nil
}

// fetchLatestReleaseRedirect follows GitHub's HTML /releases/latest redirect
// without consuming the JSON API quota. Location ends with /tag/vX.Y.Z.
func fetchLatestReleaseRedirect() (*latestReleaseInfo, error) {
	client := &http.Client{
		Timeout: 12 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequest(http.MethodGet, githubHTMLLatest, nil)
	if err != nil {
		return nil, err
	}
	applyBrowserHeaders(req)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	loc := resp.Header.Get("Location")
	if loc == "" {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 128<<10))
		if tag := extractTagFromHTML(string(body)); tag != "" {
			return &latestReleaseInfo{TagName: tag, Assets: map[string]string{}}, nil
		}
		return nil, fmt.Errorf("no Location header on /releases/latest (status %s)", resp.Status)
	}

	tag := tagFromReleaseLocation(loc)
	if tag == "" {
		return nil, fmt.Errorf("could not parse tag from Location %q", loc)
	}
	return &latestReleaseInfo{TagName: tag, Assets: map[string]string{}}, nil
}

// fetchLatestReleaseAtom reads the public Atom feed (no REST rate limit).
func fetchLatestReleaseAtom() (*latestReleaseInfo, error) {
	req, err := http.NewRequest(http.MethodGet, githubAtomFeed, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", desktopUserAgent())
	req.Header.Set("Accept", "application/atom+xml, application/xml, text/xml, */*")

	resp, err := newGitHubHTTPClient(12 * time.Second).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("atom feed status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return nil, err
	}
	tag := extractTagFromAtom(string(body))
	if tag == "" {
		// Reuse HTML tag extractor for feed body links.
		tag = extractTagFromHTML(string(body))
	}
	if tag == "" {
		return nil, fmt.Errorf("could not parse tag from atom feed")
	}
	return &latestReleaseInfo{TagName: tag, Assets: map[string]string{}}, nil
}

func extractTagFromAtom(xml string) string {
	// First entry link: <link rel="alternate" type="text/html" href=".../releases/tag/vX.Y.Z"/>
	// or <id>tag:github.com,2008:Repository/.../vX.Y.Z</id>
	const marker = "/releases/tag/"
	i := strings.Index(xml, marker)
	if i >= 0 {
		rest := xml[i+len(marker):]
		end := strings.IndexAny(rest, `"'?# \t\n\r<`)
		if end < 0 {
			end = len(rest)
			if end > 40 {
				end = 40
			}
		}
		tag := strings.TrimSpace(rest[:end])
		if tag != "" {
			return tag
		}
	}
	// Fallback: title often is "v1.2.5" or "Release v1.2.5"
	const titleOpen = "<title"
	ti := strings.Index(xml, titleOpen)
	// skip feed-level title; look for second <title> (entry)
	if ti >= 0 {
		rest := xml[ti+len(titleOpen):]
		ti2 := strings.Index(rest, titleOpen)
		if ti2 >= 0 {
			rest = rest[ti2:]
			gt := strings.Index(rest, ">")
			if gt >= 0 {
				rest = rest[gt+1:]
				close := strings.Index(rest, "</title>")
				if close > 0 {
					title := strings.TrimSpace(rest[:close])
					title = strings.TrimPrefix(title, "Release ")
					if strings.HasPrefix(title, "v") || (len(title) > 0 && title[0] >= '0' && title[0] <= '9') {
						// take first token
						if sp := strings.IndexAny(title, " \t\n"); sp > 0 {
							title = title[:sp]
						}
						return title
					}
				}
			}
		}
	}
	return ""
}

func tagFromReleaseLocation(loc string) string {
	// https://github.com/owner/repo/releases/tag/v1.2.5
	const marker = "/tag/"
	i := strings.LastIndex(loc, marker)
	if i < 0 {
		return ""
	}
	tag := loc[i+len(marker):]
	if j := strings.IndexAny(tag, "?#"); j >= 0 {
		tag = tag[:j]
	}
	return strings.Trim(tag, "/")
}

func extractTagFromHTML(html string) string {
	const marker = "/releases/tag/"
	i := strings.Index(html, marker)
	if i < 0 {
		return ""
	}
	rest := html[i+len(marker):]
	end := strings.IndexAny(rest, `"'?# \t\n\r`)
	if end < 0 {
		end = len(rest)
		if end > 32 {
			end = 32
		}
	}
	tag := strings.TrimSpace(rest[:end])
	if tag == "" {
		return ""
	}
	return tag
}

// platformArchiveAssetName returns the expected release asset file name for
// the current OS/arch, matching CI naming: membuss-<tag>-<os>-<arch>.(zip|tar.gz)
func platformArchiveAssetName(tag string) string {
	tag = strings.TrimSpace(tag)
	ext := "zip"
	if runtime.GOOS != "windows" {
		ext = "tar.gz"
	}
	return fmt.Sprintf("membuss-%s-%s-%s.%s", tag, runtime.GOOS, runtime.GOARCH, ext)
}

// platformArchiveDownloadURL builds the public download URL for the current platform.
func platformArchiveDownloadURL(tag string) string {
	name := platformArchiveAssetName(tag)
	return fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s",
		githubRepoOwner, githubRepoName, tag, name)
}

// findPlatformAssetURL returns the browser download URL for this platform from
// a release info (API assets first, then constructed public URL).
func findPlatformAssetURL(info *latestReleaseInfo) string {
	if info == nil {
		return ""
	}
	wantSuffix := fmt.Sprintf("-%s-%s.", runtime.GOOS, runtime.GOARCH)
	for name, url := range info.Assets {
		lower := strings.ToLower(name)
		if strings.Contains(lower, wantSuffix) && (strings.HasSuffix(lower, ".zip") || strings.HasSuffix(lower, ".tar.gz")) {
			return url
		}
	}
	if info.TagName != "" {
		exact := platformArchiveAssetName(info.TagName)
		if u, ok := info.Assets[exact]; ok {
			return u
		}
		return platformArchiveDownloadURL(info.TagName)
	}
	return ""
}

// platformDesktopArchiveAssetName returns the expected release asset file name for
// the desktop application on the current OS/arch.
func platformDesktopArchiveAssetName(tag string) string {
	tag = strings.TrimSpace(tag)
	ext := "zip"
	if runtime.GOOS != "windows" {
		ext = "tar.gz"
	}
	return fmt.Sprintf("membuss-desktop-%s-%s-%s.%s", tag, runtime.GOOS, runtime.GOARCH, ext)
}

// platformDesktopArchiveDownloadURL builds the public download URL for the desktop package.
func platformDesktopArchiveDownloadURL(tag string) string {
	name := platformDesktopArchiveAssetName(tag)
	return fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s",
		githubRepoOwner, githubRepoName, tag, name)
}

// findDesktopAssetURL returns the download URL for the desktop application.
func findDesktopAssetURL(info *latestReleaseInfo) string {
	if info == nil {
		return ""
	}
	wantSuffix := fmt.Sprintf("-%s-%s.", runtime.GOOS, runtime.GOARCH)
	for name, url := range info.Assets {
		lower := strings.ToLower(name)
		if strings.Contains(lower, "desktop") && strings.Contains(lower, wantSuffix) &&
			(strings.HasSuffix(lower, ".zip") || strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".appimage")) {
			return url
		}
	}
	// Also check if any asset matches AppImage or desktop installer
	for name, url := range info.Assets {
		lower := strings.ToLower(name)
		if strings.Contains(lower, "membuss") && strings.Contains(lower, wantSuffix) &&
			(strings.HasSuffix(lower, ".appimage") || strings.Contains(lower, "desktop")) {
			return url
		}
	}
	if info.TagName != "" {
		exact := platformDesktopArchiveAssetName(info.TagName)
		if u, ok := info.Assets[exact]; ok {
			return u
		}
		return platformDesktopArchiveDownloadURL(info.TagName)
	}
	return ""
}

// findDaemonAssetURL returns the download URL for the daemon binary package.
func findDaemonAssetURL(info *latestReleaseInfo) string {
	return findPlatformAssetURL(info)
}

// parseReleaseTagOrURL extracts a valid semantic tag (e.g. "v2.8.4") from
// a GitHub release URL, download URL, or user input string.
func parseReleaseTagOrURL(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}

	// 1. If it's a GitHub /releases/tag/vX.Y.Z URL
	if idx := strings.Index(input, "/releases/tag/"); idx >= 0 {
		rest := input[idx+len("/releases/tag/"):]
		if end := strings.IndexAny(rest, "/?# \t\n\r"); end >= 0 {
			rest = rest[:end]
		}
		return strings.TrimSpace(rest)
	}

	// 2. If it's a GitHub /releases/download/vX.Y.Z/... URL
	if idx := strings.Index(input, "/releases/download/"); idx >= 0 {
		rest := input[idx+len("/releases/download/"):]
		if end := strings.Index(rest, "/"); end >= 0 {
			rest = rest[:end]
		}
		return strings.TrimSpace(rest)
	}

	// 3. If it's a raw tag or version string: e.g. "v2.8.4" or "2.8.4"
	clean := strings.TrimPrefix(input, "refs/tags/")
	clean = strings.TrimSpace(clean)
	if len(clean) > 0 && clean[0] >= '0' && clean[0] <= '9' {
		clean = "v" + clean
	}
	return clean
}

// isValidSemverTag verifies that the string matches a valid semantic version tag (e.g. v2.8.4, 2.8.4, v1.0.0-rc1).
func isValidSemverTag(tag string) bool {
	clean := strings.TrimPrefix(strings.TrimSpace(tag), "v")
	if clean == "" {
		return false
	}
	// Must start with digit
	if clean[0] < '0' || clean[0] > '9' {
		return false
	}
	// Verify characters: digits, dots, hyphens, alphanumeric
	for _, r := range clean {
		if (r < '0' || r > '9') && r != '.' && r != '-' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

// fetchReleaseByTag fetches release details for a specific tag with strict existence checking.
func fetchReleaseByTag(tag string) (*latestReleaseInfo, error) {
	tag = parseReleaseTagOrURL(tag)
	if tag == "" || !isValidSemverTag(tag) {
		return nil, fmt.Errorf("invalid release version format '%s': expected semantic format like v2.8.4", tag)
	}

	// 1. Try GitHub REST API
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s", githubRepoOwner, githubRepoName, tag)
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err == nil {
		applyGitHubAPIHeaders(req)
		resp, err := newGitHubHTTPClient(10 * time.Second).Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
				var release map[string]any
				if err := json.Unmarshal(body, &release); err == nil {
					info := &latestReleaseInfo{
						TagName: tag,
						Assets:  make(map[string]string),
						FromAPI: true,
					}
					if assets, ok := release["assets"].([]any); ok {
						for _, a := range assets {
							m, ok := a.(map[string]any)
							if !ok {
								continue
							}
							name, _ := m["name"].(string)
							url, _ := m["browser_download_url"].(string)
							if name != "" && url != "" {
								info.Assets[name] = url
							}
						}
					}
					return info, nil
				}
			} else if resp.StatusCode == http.StatusNotFound {
				return nil, fmt.Errorf("release '%s' was not found on GitHub", tag)
			}
		}
	}

	// 2. Fallback: Verify release HTML page on GitHub (for rate-limited API calls)
	htmlURL := fmt.Sprintf("https://github.com/%s/%s/releases/tag/%s", githubRepoOwner, githubRepoName, tag)
	hreq, err := http.NewRequest(http.MethodGet, htmlURL, nil)
	if err == nil {
		applyBrowserHeaders(hreq)
		hresp, err := newGitHubHTTPClient(10 * time.Second).Do(hreq)
		if err == nil {
			defer hresp.Body.Close()
			if hresp.StatusCode == http.StatusOK {
				info := &latestReleaseInfo{
					TagName: tag,
					Assets:  make(map[string]string),
					FromAPI: false,
				}
				daemonName := platformArchiveAssetName(tag)
				info.Assets[daemonName] = platformArchiveDownloadURL(tag)
				desktopName := platformDesktopArchiveAssetName(tag)
				info.Assets[desktopName] = platformDesktopArchiveDownloadURL(tag)
				return info, nil
			} else if hresp.StatusCode == http.StatusNotFound {
				return nil, fmt.Errorf("release '%s' does not exist on GitHub", tag)
			}
		}
	}

	return nil, fmt.Errorf("release '%s' could not be verified on GitHub", tag)
}

func abbreviate(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

