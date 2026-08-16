package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/nnlgsakib/membuss/core/version"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx           context.Context
	daemonManager *DaemonManager
	config        *DesktopConfig
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods.
// The daemon is NOT auto-started — the user must click Start Node.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	CleanupOldExecutables()
	cfg, err := LoadConfig()
	if err != nil {
		wailsRuntime.LogErrorf(ctx, "failed to load config: %v", err)
		cfg = &DesktopConfig{}
	}
	// The running desktop application's source of truth is always the compiled version.Version
	cfg.InstalledVersion = "v" + strings.TrimPrefix(version.Version, "v")
	a.config = cfg
	a.daemonManager = NewDaemonManager(cfg)
}

// GetConfig returns the current desktop config settings.
func (a *App) GetConfig() *DesktopConfig {
	return a.config
}

// SaveConfig updates and saves the desktop config.
func (a *App) SaveConfig(cfg *DesktopConfig) error {
	a.config.DataDir = cfg.DataDir
	a.config.SetupComplete = cfg.SetupComplete
	a.config.GRPCAddr = cfg.GRPCAddr
	a.config.APIAddr = cfg.APIAddr
	a.config.GatewayAddr = cfg.GatewayAddr
	a.config.KeepAlive = cfg.KeepAlive
	a.config.AutoStart = cfg.AutoStart
	a.config.InstalledVersion = cfg.InstalledVersion
	return a.config.Save()
}

// SelectDirectory opens the OS-native directory picker.
func (a *App) SelectDirectory() (string, error) {
	dir, err := wailsRuntime.OpenDirectoryDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "Choose Membuss Data Directory",
	})
	if err != nil {
		return "", err
	}
	if dir == "" {
		return "", errors.New("directory selection cancelled")
	}
	return dir, nil
}

// OpenDataDir opens the configured data directory in the OS file explorer.
func (a *App) OpenDataDir() error {
	dir := a.config.DataDir
	if dir == "" {
		return errors.New("no data directory configured")
	}
	switch runtime.GOOS {
	case "windows":
		return exec.Command("explorer", dir).Start()
	case "darwin":
		return exec.Command("open", dir).Start()
	default:
		return exec.Command("xdg-open", dir).Start()
	}
}

// InstallBinaries downloads and extracts the daemon binaries, emitting progress events.
func (a *App) InstallBinaries(targetDir string) error {
	if targetDir == "" {
		return errors.New("target directory cannot be empty")
	}

	progressCallback := func(percent int, msg string) {
		wailsRuntime.EventsEmit(a.ctx, "install_progress", map[string]any{
			"percent": percent,
			"message": msg,
		})
	}

	// Run installation in background
	go func() {
		versionTag, err := a.daemonManager.DownloadLatestRelease(targetDir, progressCallback)
		if err != nil {
			wailsRuntime.EventsEmit(a.ctx, "install_progress", map[string]any{
				"percent": -1, // Indicates error
				"message": err.Error(),
			})
			return
		}

		// Save the folder and set setup as complete
		a.config.DataDir = targetDir
		a.config.InstalledVersion = versionTag
		a.config.SetupComplete = true
		_ = a.config.Save()
	}()

	return nil
}

// GetNodeConfig returns the config.yaml values in a key-value format.
func (a *App) GetNodeConfig() (map[string]any, error) {
	if a.config.DataDir == "" {
		return nil, errors.New("no data directory configured")
	}
	return LoadYamlConfig(a.config.DataDir)
}

// SaveNodeConfig serializes config.yaml updates.
func (a *App) SaveNodeConfig(cfg map[string]any) error {
	if a.config.DataDir == "" {
		return errors.New("no data directory configured")
	}
	return SaveYamlConfig(a.config.DataDir, cfg)
}

// GetNodeConfigRaw returns the raw YAML content of config.yaml.
func (a *App) GetNodeConfigRaw() (string, error) {
	if a.config.DataDir == "" {
		return "", errors.New("no data directory configured")
	}
	path := filepath.Join(a.config.DataDir, "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// SaveNodeConfigRaw writes raw YAML content to config.yaml.
func (a *App) SaveNodeConfigRaw(content string) error {
	if a.config.DataDir == "" {
		return errors.New("no data directory configured")
	}

	// Normalize Windows backslashes to forward slashes in path fields to prevent escape sequence parse errors in YAML
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "geolocation_db:") ||
			strings.HasPrefix(trimmed, "data_dir:") ||
			strings.HasPrefix(trimmed, "cert_file:") ||
			strings.HasPrefix(trimmed, "key_file:") {
			lines[i] = strings.ReplaceAll(line, "\\", "/")
		}
	}
	content = strings.Join(lines, "\n")

	path := filepath.Join(a.config.DataDir, "config.yaml")
	return os.WriteFile(path, []byte(content), 0644)
}

// WriteDefaultConfig generates a complete config.yaml with all default fields.
func (a *App) WriteDefaultConfig() error {
	if a.config.DataDir == "" {
		return errors.New("no data directory configured")
	}
	return WriteDefaultConfig(a.config.DataDir)
}

// StartNode launches the daemon.
func (a *App) StartNode() error {
	if a.config.DataDir == "" {
		return errors.New("no data directory configured")
	}
	// If a keep-alive instance is already healthy, treat as success.
	if _, err := a.daemonManager.CheckStatus(a.config.APIAddr, a.config.GatewayAddr); err == nil {
		return nil
	}
	return a.daemonManager.Start(a.config.DataDir)
}

// StopNode terminates the daemon.
func (a *App) StopNode() error {
	return a.daemonManager.Stop()
}

// CheckNodeStatus checks if the daemon is online and queries Node Info.
func (a *App) CheckNodeStatus() (map[string]any, error) {
	// 1. Probe the HTTP API and Gateway (fast, authoritative, zero subprocesses)
	info, err := a.daemonManager.CheckStatus(a.config.APIAddr, a.config.GatewayAddr)
	apiOnline := err == nil

	isRunning := apiOnline
	if !isRunning {
		// 2. Fast in-memory process handle check
		isRunning = a.daemonManager.IsRunning()
	}

	statusMap := map[string]any{
		"process_running": isRunning,
		"api_online":      apiOnline,
	}

	if apiOnline {
		statusMap["info"] = info
	} else if isRunning && err != nil {
		statusMap["error"] = err.Error()
	}

	return statusMap, nil
}

// CheckExplorer checks if the gateway's explorer is online.
func (a *App) CheckExplorer() bool {
	return a.daemonManager.CheckExplorer(a.config.GatewayAddr)
}

// VerifyInstallation checks if the installed binaries and configurations are intact.
func (a *App) VerifyInstallation() map[string]any {
	cfg := a.config
	result := map[string]any{
		"valid":          true,
		"data_dir_ok":    true,
		"daemon_bin_ok":  true,
		"cli_bin_ok":     true,
		"node_config_ok": true,
	}

	if !cfg.SetupComplete {
		result["valid"] = false
		return result
	}

	// 1. Check DataDir exists
	if cfg.DataDir == "" {
		result["valid"] = false
		result["data_dir_ok"] = false
		return result
	}

	if fi, err := os.Stat(cfg.DataDir); err != nil || !fi.IsDir() {
		result["valid"] = false
		result["data_dir_ok"] = false
	}

	// 2. Check daemon binary (membuss is the unified daemon + CLI binary)
	exeName := "membuss"
	if runtime.GOOS == "windows" {
		exeName = "membuss.exe"
	}
	daemonPath := filepath.Join(cfg.DataDir, "bin", exeName)
	if _, err := os.Stat(daemonPath); err != nil {
		if localBinDir, err := findLocalBinaries(); err == nil {
			daemonPath = filepath.Join(localBinDir, exeName)
		} else {
			daemonPath = ""
		}
	}
	if daemonPath == "" {
		result["valid"] = false
		result["daemon_bin_ok"] = false
		result["cli_bin_ok"] = false
	}

	// 3. Check node config.yaml
	configYamlPath := filepath.Join(cfg.DataDir, "config.yaml")
	if _, err := os.Stat(configYamlPath); err != nil {
		result["valid"] = false
		result["node_config_ok"] = false
	}

	return result
}

// ResetSetup clears the config and stops the node.
func (a *App) ResetSetup() error {
	// 1. Stop node if running
	_ = a.daemonManager.Stop()

	// 2. Reset config fields
	a.config.SetupComplete = false
	a.config.DataDir = ""

	// 3. Save reset state
	return a.config.Save()
}

var ansiRegexp = regexp.MustCompile(`(?:\x1b|\x1B)\[[0-9;?]*[ -/]*[@-~]|\[[0-9]{1,2}(?:;[0-9]{1,2})*m`)

func cleanANSI(s string) string {
	return ansiRegexp.ReplaceAllString(s, "")
}

// GetDaemonLogs reads the last few lines from the daemon log file or memory buffer.
func (a *App) GetDaemonLogs() (string, error) {
	// 1. Fast in-memory buffer check
	if buffered := a.daemonManager.GetBufferedLogs(); len(buffered) > 0 {
		return cleanANSI(strings.Join(buffered, "\n")), nil
	}

	if a.config.DataDir == "" {
		return "", errors.New("no data directory configured")
	}
	logPath := filepath.Join(a.config.DataDir, "logs", "daemon.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "No daemon logs found yet.", nil
		}
		return "", err
	}

	// Limit to last 64KB of log data to keep frontend performance optimal
	const maxBytes = 64 * 1024
	var logStr string
	if len(data) > maxBytes {
		logStr = string(data[len(data)-maxBytes:])
	} else {
		logStr = string(data)
	}

	return cleanANSI(logStr), nil
}

// ClearDaemonLogs clears both the in-memory log buffer and the daemon.log file on disk.
func (a *App) ClearDaemonLogs() error {
	if a.daemonManager != nil && a.daemonManager.logBuffer != nil {
		a.daemonManager.logBuffer.Clear()
	}
	if a.config.DataDir != "" {
		logPath := filepath.Join(a.config.DataDir, "logs", "daemon.log")
		_ = os.WriteFile(logPath, []byte(""), 0644)
	}
	return nil
}

// domReady is called when the renderer has loaded.
func (a *App) domReady(ctx context.Context) {
	// Custom hooks if needed on DOM load
}

// beforeClose is triggered when the window is closed.
// KeepAlive: leave the daemon running in the background (no stop, no prompt).
// Otherwise: stop the daemon (if any) and allow the window to close.
func (a *App) beforeClose(ctx context.Context) bool {
	if a.config != nil && a.config.KeepAlive && a.config.SetupComplete {
		// Detached daemon continues; do not kill on GUI exit.
		wailsRuntime.LogInfo(ctx, "keep_alive enabled — closing GUI without stopping daemon")
		return false
	}

	// Stop tracked + any system-wide daemon when keep-alive is off.
	_ = a.daemonManager.Stop()
	return false
}

// UpdateCheckResult holds the version check status.
type UpdateCheckResult struct {
	HasUpdate      bool   `json:"has_update"`
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
}

// CheckForUpdate queries GitHub for the latest release and compares with the installed version.
// Uses the REST API with an automatic /releases/latest redirect fallback so anonymous
// clients still work after API rate limits (HTTP 403).
func (a *App) CheckForUpdate() (*UpdateCheckResult, error) {
	// 1. Determine current version
	currentVer := a.config.InstalledVersion
	if currentVer == "" {
		exeExt := ""
		if runtime.GOOS == "windows" {
			exeExt = ".exe"
		}
		binPath := filepath.Join(a.config.DataDir, "bin", "membuss"+exeExt)
		currentVer = getInstalledBinaryVersion(binPath)
		if currentVer == "" {
			currentVer = version.Version
		}
	}
	currentVer = strings.TrimPrefix(currentVer, "v")

	// 2. Fetch latest release (API → HTML redirect fallback)
	info, err := fetchLatestRelease()
	if err != nil {
		return nil, fmt.Errorf("failed to check for updates: %w", err)
	}

	latestVer := info.TagName
	if latestVer == "" {
		return nil, fmt.Errorf("failed to check for updates: empty release tag")
	}
	latestVerClean := strings.TrimPrefix(latestVer, "v")
	hasUpdate := isVersionNewer(currentVer, latestVerClean)

	// Normalize latest for display (always v-prefixed).
	displayLatest := latestVer
	if !strings.HasPrefix(displayLatest, "v") {
		displayLatest = "v" + displayLatest
	}

	return &UpdateCheckResult{
		HasUpdate:      hasUpdate,
		CurrentVersion: "v" + currentVer,
		LatestVersion:  displayLatest,
	}, nil
}

// getInstalledBinaryVersion runs the installed CLI binary and parses its version string.
func getInstalledBinaryVersion(cliPath string) string {
	if _, err := os.Stat(cliPath); err != nil {
		return ""
	}
	cmd := exec.Command(cliPath, "version")
	hideConsoleWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "membuss version") {
			parts := strings.Fields(line)
			for i, part := range parts {
				if part == "version" && i+1 < len(parts) {
					return parts[i+1]
				}
			}
		}
	}
	return ""
}

// InspectReleaseURL inspects a version tag or release URL and returns release metadata.
func (a *App) InspectReleaseURL(urlOrTag string) (map[string]any, error) {
	tag := parseReleaseTagOrURL(urlOrTag)

	var info *latestReleaseInfo
	var err error

	if tag == "latest" || tag == "" {
		info, err = fetchLatestRelease()
	} else {
		info, err = fetchReleaseByTag(tag)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to resolve release: %w", err)
	}
	if info == nil {
		return nil, fmt.Errorf("release information unavailable")
	}

	currentVer := "v" + strings.TrimPrefix(version.Version, "v")
	if a.config != nil && a.config.InstalledVersion != "" {
		a.config.InstalledVersion = currentVer
	}

	resolvedTag := version.Version
	if info != nil && info.TagName != "" {
		resolvedTag = info.TagName
	}

	daemonURL := ""
	desktopURL := ""
	if info != nil {
		daemonURL = findDaemonAssetURL(info)
		desktopURL = findDesktopAssetURL(info)
	}

	return map[string]any{
		"tag_name":        resolvedTag,
		"current_version": currentVer,
		"is_newer":        isVersionNewer(resolvedTag, currentVer),
		"is_older":        isVersionNewer(currentVer, resolvedTag),
		"is_same":         resolvedTag == currentVer || strings.TrimPrefix(resolvedTag, "v") == strings.TrimPrefix(currentVer, "v"),
		"daemon_url":      daemonURL,
		"desktop_url":     desktopURL,
		"from_api":        info != nil && info.FromAPI,
		"assets_count":    len(info.Assets),
	}, nil
}

// InstallComponents installs the selected components (Core Daemon and/or Desktop GUI) for the given tag/URL.
func (a *App) InstallComponents(urlOrTag string, installCore bool, installDesktop bool) error {
	if a.config.DataDir == "" {
		return errors.New("no data directory configured")
	}

	// 1. Stop the node and force-kill system-wide to ensure files are not locked
	wailsRuntime.LogInfo(a.ctx, "stopping node and force killing processes before installing release...")
	_ = a.daemonManager.Stop()
	_ = killProcess("membuss*")
	time.Sleep(500 * time.Millisecond)

	opts := InstallReleaseOptions{
		TagOrURL:       urlOrTag,
		InstallCore:    installCore,
		InstallDesktop: installDesktop,
	}

	progressCallback := func(percent int, msg string) {
		wailsRuntime.EventsEmit(a.ctx, "upgrade_progress", map[string]any{
			"percent": percent,
			"message": msg,
		})
	}

	go func() {
		versionTag, err := a.daemonManager.InstallCustomRelease(a.config.DataDir, opts, progressCallback)
		if err != nil {
			wailsRuntime.EventsEmit(a.ctx, "upgrade_progress", map[string]any{
				"percent": -1,
				"message": err.Error(),
			})
			return
		}

		// Update config with installed version
		a.config.InstalledVersion = versionTag
		_ = a.config.Save()

		// Send success event
		wailsRuntime.EventsEmit(a.ctx, "upgrade_progress", map[string]any{
			"percent": 100,
			"message": fmt.Sprintf("Installation of %s complete!", versionTag),
		})
	}()

	return nil
}

// UpgradeBinaries stops the node, removes the old binaries, downloads the latest release, and updates config.
func (a *App) UpgradeBinaries() error {
	return a.InstallComponents("", true, true)
}

// RelaunchApp restarts the desktop application using the newly installed binary.
func (a *App) RelaunchApp() error {
	sum := NewSelfUpdateManager()
	return sum.RelaunchApp()
}

// IsNodeRunningSystemWide checks if any membuss daemon process is running on the system.
func (a *App) IsNodeRunningSystemWide() bool {
	return a.daemonManager.IsRunning() || isProcessRunning("membuss")
}

// DownloadContent fetches a Membuss gateway URL and saves the response
// body into dir, auto-detecting the filename from the Content-Disposition
// header (or the URL path). This powers the desktop "Download to folder"
// flow so explorer downloads land in a user-chosen directory instead of
// the default Downloads folder.
// 🖂 minimal: one-shot stream to disk, no resume/partial support.
func (a *App) DownloadContent(targetURL, dir string) (string, error) {
	if dir == "" {
		return "", errors.New("destination directory cannot be empty")
	}
	// 🖂 only allow the local gateway to avoid fetching arbitrary URLs.
	gw := "http://" + a.config.GatewayAddr
	if !strings.HasPrefix(targetURL, gw) &&
		!strings.HasPrefix(targetURL, "http://127.0.0.1") &&
		!strings.HasPrefix(targetURL, "http://localhost") {
		return "", errors.New("only local gateway URLs are supported")
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("invalid destination directory: %w", err)
	}

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(targetURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: status %s", resp.Status)
	}

	name := detectDownloadFilename(resp, targetURL)
	outPath := filepath.Join(dir, name)
	f, err := os.Create(outPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", err
	}
	return outPath, nil
}

// detectDownloadFilename derives a safe filename from the
// Content-Disposition header, falling back to the URL path.
func detectDownloadFilename(resp *http.Response, fallbackURL string) string {
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if fn := parseDispositionFilename(cd); fn != "" {
			return sanitizeDownloadName(fn)
		}
	}
	if u, err := url.Parse(fallbackURL); err == nil {
		if base := filepath.Base(u.Path); base != "" && base != "/" && base != "." && base != `\` {
			return sanitizeDownloadName(base)
		}
	}
	return "download.bin"
}

// parseDispositionFilename extracts the filename from a
// Content-Disposition header, supporting both filename= and the
// RFC 5987 filename*=UTF-8''encoded form.
func parseDispositionFilename(cd string) string {
	// RFC 5987 extended form first (highest precedence).
	for _, part := range strings.Split(cd, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(strings.ToLower(part), "filename*=") {
			val := part[len("filename*="):]
			val = strings.Trim(val, `"`)
			// Expect form: UTF-8''encoded
			if i := strings.Index(val, "''"); i >= 0 {
				if dec, err := url.QueryUnescape(val[i+2:]); err == nil {
					return dec
				}
			}
		}
	}
	for _, part := range strings.Split(cd, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(strings.ToLower(part), "filename=") {
			val := part[len("filename="):]
			return strings.Trim(val, `"'`)
		}
	}
	return ""
}

// sanitizeDownloadName strips path separators and control characters
// so a malicious/disallowed name cannot escape the destination dir.
func sanitizeDownloadName(name string) string {
	name = filepath.Base(name)
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '_'
		}
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		}
		return r
	}, name)
}

// ForceKillNode attempts to force-kill any running membuss daemon processes on the system.
func (a *App) ForceKillNode() error {
	wailsRuntime.LogInfo(a.ctx, "force killing node daemon processes...")
	_ = a.daemonManager.Stop()
	_ = killProcess("membuss*")
	time.Sleep(500 * time.Millisecond)
	return nil
}

// isVersionNewer helper function to compare two semantic versions.
func isVersionNewer(current, latest string) bool {
	current = strings.TrimPrefix(strings.TrimSpace(current), "v")
	latest = strings.TrimPrefix(strings.TrimSpace(latest), "v")
	if current == "" {
		return latest != ""
	}
	if latest == "" {
		return false
	}
	cParts := strings.Split(current, ".")
	lParts := strings.Split(latest, ".")
	for i := 0; i < len(cParts) && i < len(lParts); i++ {
		var cVal, lVal int
		fmt.Sscanf(cParts[i], "%d", &cVal)
		fmt.Sscanf(lParts[i], "%d", &lVal)
		if lVal > cVal {
			return true
		}
		if lVal < cVal {
			return false
		}
	}
	return len(lParts) > len(cParts)
}

// isProcessRunning checks if a process with the given name is active on the host system.
func isProcessRunning(name string) bool {
	if runtime.GOOS == "windows" {
		if !strings.HasSuffix(name, ".exe") {
			name += ".exe"
		}
		cmd := exec.Command("tasklist", "/FI", fmt.Sprintf("IMAGENAME eq %s", name), "/NH")
		hideConsoleWindow(cmd)
		out, err := cmd.Output()
		if err != nil {
			return false
		}
		return strings.Contains(strings.ToLower(string(out)), strings.ToLower(name))
	} else {
		cmd := exec.Command("pgrep", "-x", name)
		hideConsoleWindow(cmd)
		err := cmd.Run()
		return err == nil
	}
}

// killProcess kills all processes matching the given name, excluding the current process.
func killProcess(name string) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		if !strings.HasSuffix(name, ".exe") && !strings.Contains(name, "*") {
			name += ".exe"
		}
		ourPid := fmt.Sprintf("%d", os.Getpid())
		cmd = exec.Command("taskkill", "/F", "/FI", "PID ne "+ourPid, "/IM", name)
		hideConsoleWindow(cmd)
		return cmd.Run()
	}

	// On non-Windows: run pgrep to find PIDs matching the exact process name
	pgrep := exec.Command("pgrep", "-x", name)
	out, err := pgrep.Output()
	if err != nil {
		return nil // no matching processes or pgrep not found
	}

	ourPid := os.Getpid()
	pids := strings.Split(string(out), "\n")
	for _, pidStr := range pids {
		pidStr = strings.TrimSpace(pidStr)
		if pidStr == "" {
			continue
		}
		var pid int
		if _, err := fmt.Sscanf(pidStr, "%d", &pid); err == nil {
			if pid != ourPid {
				// Kill the process
				if proc, err := os.FindProcess(pid); err == nil {
					_ = proc.Kill()
				}
			}
		}
	}
	return nil
}

