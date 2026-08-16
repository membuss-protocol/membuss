package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/nnlgsakib/membuss/config"
	"github.com/nnlgsakib/membuss/core/version"
	"gopkg.in/yaml.v3"
)

// DesktopConfig stores the GUI application configurations.
type DesktopConfig struct {
	DataDir          string `json:"data_dir"`
	SetupComplete    bool   `json:"setup_complete"`
	GRPCAddr         string `json:"grpc_addr"`
	APIAddr          string `json:"api_addr"`
	GatewayAddr      string `json:"gateway_addr"`
	KeepAlive        bool   `json:"keep_alive"` // Keep daemon running when GUI closes
	AutoStart        bool   `json:"auto_start"` // Deprecated: ignored; node starts only via Start Node
	InstalledVersion string `json:"installed_version"`
}

// GetConfigPath returns the persistent configuration path:
// Windows: %APPDATA%/Membuss/desktop-config.json
// macOS/Linux: ~/.config/membuss/desktop-config.json
func GetConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	appDir := filepath.Join(dir, "Membuss")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		return "", err
	}
	return filepath.Join(appDir, "desktop-config.json"), nil
}

// LoadConfig loads the GUI settings.
func LoadConfig() (*DesktopConfig, error) {
	path, err := GetConfigPath()
	if err != nil {
		return nil, err
	}

	cfg := &DesktopConfig{
		GRPCAddr:    "127.0.0.1:50051",
		APIAddr:     "127.0.0.1:5001",
		GatewayAddr: "127.0.0.1:8080",
		KeepAlive:   true,
		AutoStart:   false, // never auto-start; Start/Stop button only
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil // Return defaults
		}
		return nil, err
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	// Force off: daemon is only started via the dashboard Start Node button.
	cfg.AutoStart = false
	return cfg, nil
}

// Save saves the GUI settings.
func (c *DesktopConfig) Save() error {
	path, err := GetConfigPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// LogRingBuffer stores the most recent N lines of daemon logs in memory.
type LogRingBuffer struct {
	mu       sync.Mutex
	lines    []string
	maxLines int
}

func NewLogRingBuffer(maxLines int) *LogRingBuffer {
	if maxLines <= 0 {
		maxLines = 600
	}
	return &LogRingBuffer{
		lines:    make([]string, 0, maxLines),
		maxLines: maxLines,
	}
}

func (rb *LogRingBuffer) Write(p []byte) (n int, err error) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	str := string(p)
	parts := strings.Split(str, "\n")
	for _, part := range parts {
		trimmed := strings.TrimRight(part, "\r")
		if trimmed != "" {
			if len(rb.lines) >= rb.maxLines {
				rb.lines = rb.lines[1:]
			}
			rb.lines = append(rb.lines, trimmed)
		}
	}
	return len(p), nil
}

func (rb *LogRingBuffer) GetLines() []string {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	out := make([]string, len(rb.lines))
	copy(out, rb.lines)
	return out
}

func (rb *LogRingBuffer) Clear() {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.lines = rb.lines[:0]
}

// DaemonManager manages the background daemon process.
type DaemonManager struct {
	mu        sync.Mutex
	cmd       *exec.Cmd
	config    *DesktopConfig
	ctx       context.Context
	logBuffer *LogRingBuffer
}

func NewDaemonManager(cfg *DesktopConfig) *DaemonManager {
	return &DaemonManager{
		config:    cfg,
		logBuffer: NewLogRingBuffer(600),
	}
}

func (dm *DaemonManager) GetBufferedLogs() []string {
	if dm.logBuffer == nil {
		return nil
	}
	return dm.logBuffer.GetLines()
}

// IsRunning reports whether a usable daemon is currently running.
//
// The HTTP API is the source of truth: if it answers, the daemon is
// genuinely up. Process scans (pid file, pgrep/tasklist) only serve as a
// secondary signal for the tracked-child and keep-alive cases where the
// API port in the desktop config may be stale.
//
// This deliberately does NOT treat a bare process-name match as "running"
// on its own — that was the source of the historical false-positive
// "daemon is already running": a stale daemon.pid pointing at a recycled
// PID (now some unrelated process) or a zombie/defunct daemon from a
// crashed previous session would block Start() forever. isMembussPidAlive
// verifies the image name so PID reuse can never trip the check, and
// cleanupStaleState (called by Start) reaps half-dead daemons before the
// already-running gate is evaluated.
func (dm *DaemonManager) IsRunning() bool {
	// 1. Tracked child from THIS GUI session, still in flight.
	//    Counts as running even before the API is ready, so a rapid
	//    second Start() won't spawn a duplicate that fails to bind ports.
	dm.mu.Lock()
	cmd := dm.cmd
	dm.mu.Unlock()
	if cmd != nil && cmd.Process != nil && cmd.ProcessState == nil {
		return true
	}

	// 2. Authoritative API probe — covers keep-alive after a GUI restart,
	//    an orphan from a previous session, or any untracked healthy daemon.
	if dm.apiHealthy() {
		return true
	}

	// 3. Pid file verified against the process image name. Catches the
	//    keep-alive case where the daemon is up on config.yaml's port but
	//    the desktop config still holds a stale port. A stale/foreign pid
	//    file (PID reused by a non-membuss process) is cleaned up rather
	//    than trusted.
	if pid, ok := dm.readPidFile(); ok {
		if isMembussPidAlive(pid) {
			return true
		}
		dm.removePidFile()
	}

	return false
}

// apiHealthy reports whether the daemon HTTP API responds with a healthy
// status. This is the authoritative liveness signal: a process that
// exists but does not answer its API is not usable and should not block
// a restart.
//
// A short timeout is used because this is called on the hot Start() path;
// CheckStatus (used by the UI for full node info) keeps its longer timeout.
func (dm *DaemonManager) apiHealthy() bool {
	if dm.config == nil || dm.config.APIAddr == "" {
		return false
	}
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://%s/api/v1/node/info", dm.config.APIAddr))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	// Drain so the connection can be reused by the keep-alive transport.
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

// cleanupStaleState removes any leftover daemon state that could block a
// clean Start():
//   - a stale daemon.pid whose PID is dead or belongs to a non-membuss
//     process (PID reuse),
//   - a half-dead membuss process whose HTTP API is not responding
//     (so a crashed/looping daemon doesn't refuse restart forever).
//
// It is deliberately tolerant: any error is swallowed because the caller
// (Start) should proceed to attempt launching regardless — failing to
// clean up is never a reason to wedge the user out of their node.
func (dm *DaemonManager) cleanupStaleState(dataDir string) {
	// If the API is genuinely up, there is nothing to clean.
	if dm.apiHealthy() {
		return
	}

	// Stale / foreign pid file, or a wedged membuss process behind it.
	if pid, ok := dm.readPidFile(); ok {
		if !isMembussPidAlive(pid) {
			// PID is dead or belongs to something else — drop the file.
			dm.removePidFile()
		} else {
			// PID is a membuss process but the API is down — it's wedged.
			// Kill it so we can restart cleanly, then wait for the OS to
			// reap it so the ports are released before we bind again.
			_ = killPid(pid)
			dm.waitPidGone(pid, 4*time.Second)
			dm.removePidFile()
		}
	}

	// Any other lingering membuss processes whose API is down. Safe here
	// because we already confirmed the API is not responding, so this
	// only hits dead/stuck instances — never a healthy daemon.
	_ = killProcess("membuss")
	_ = killProcess("membuss-cli")
}

// readPidFile reads and parses daemon.pid from the configured data dir.
// Returns (pid, ok); ok is false if the file is missing, unreadable, or
// does not contain a positive integer.
func (dm *DaemonManager) readPidFile() (int, bool) {
	if dm.config == nil || dm.config.DataDir == "" {
		return 0, false
	}
	pidPath := filepath.Join(dm.config.DataDir, "daemon.pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return 0, false
	}
	var pid int
	if _, err := fmt.Sscanf(string(data), "%d", &pid); err != nil {
		return 0, false
	}
	if pid <= 0 {
		return 0, false
	}
	return pid, true
}

// removePidFile deletes daemon.pid if it exists. Errors are ignored — a
// missing or unremovable file is not fatal for the restart flow.
func (dm *DaemonManager) removePidFile() {
	if dm.config == nil || dm.config.DataDir == "" {
		return
	}
	_ = os.Remove(filepath.Join(dm.config.DataDir, "daemon.pid"))
}

// waitPidGone polls until the given PID is no longer a membuss process or
// the timeout elapses. Used after killing a wedged daemon to give the OS
// time to release the listening sockets before a fresh daemon binds them.
func (dm *DaemonManager) waitPidGone(pid int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !isMembussPidAlive(pid) && !isPidActive(pid) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Start spawns the daemon in the background.
func (dm *DaemonManager) Start(dataDir string) error {
	// 1. Self-heal first: clear stale pid files and reap half-dead
	//    daemons so a crashed previous session never blocks this Start().
	//    This runs before the already-running check so the user is never
	//    wedged by a stale daemon.pid or a zombie membuss process.
	dm.cleanupStaleState(dataDir)

	// 2. Refuse only if a genuinely usable daemon is already up. Because
	//    cleanupStaleState already reaped anything whose API was down,
	//    reaching this branch means the API answered — a real running node.
	if dm.IsRunning() {
		return errors.New("daemon is already running")
	}

	// 3. Resolve any blocked ports dynamically. Done after the
	//    already-running check so we never shift a live daemon's ports
	//    out from under it.
	if err := dm.resolveBlockedPorts(dataDir); err != nil {
		// Log error but continue trying to start
	}

	dm.mu.Lock()
	defer dm.mu.Unlock()

	exeName := "membuss"
	if runtime.GOOS == "windows" {
		exeName = "membuss.exe"
	}
	binaryPath := filepath.Join(dataDir, "bin", exeName)

	if _, err := os.Stat(binaryPath); err != nil {
		return fmt.Errorf("daemon binary not found at %s. Please run setup first", binaryPath)
	}

	configPath := filepath.Join(dataDir, "config.yaml")

	// Create / append a log file inside the data directory
	logDir := filepath.Join(dataDir, "logs")
	_ = os.MkdirAll(logDir, 0755)
	logFile, err := os.OpenFile(filepath.Join(logDir, "daemon.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		sessionBanner := fmt.Sprintf("\n=== MEMBUSS DAEMON SESSION STARTED AT %s ===\n", time.Now().Format("2006-01-02 15:04:05"))
		_, _ = logFile.WriteString(sessionBanner)
		if dm.logBuffer != nil {
			_, _ = dm.logBuffer.Write([]byte(sessionBanner))
		}
	}

	startCmd := func(detached bool) (*exec.Cmd, error) {
		c := exec.Command(binaryPath, "-datadir", dataDir, "-config", configPath)
		if detached {
			hideConsoleWindow(c)
		} else {
			hideConsoleWindowSimple(c)
		}
		var writers []io.Writer
		if logFile != nil {
			writers = append(writers, logFile)
		}
		if dm.logBuffer != nil {
			writers = append(writers, dm.logBuffer)
		}
		if len(writers) > 0 {
			mw := io.MultiWriter(writers...)
			c.Stdout = mw
			c.Stderr = mw
		}
		return c, c.Start()
	}

	// Prefer a breakaway/detached child so keep-alive survives GUI exit.
	cmd, err := startCmd(true)
	if err != nil {
		// Some Windows job policies reject CREATE_BREAKAWAY_FROM_JOB.
		cmd, err = startCmd(false)
	}
	if err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	dm.cmd = cmd
	pidPath := filepath.Join(dataDir, "daemon.pid")
	_ = os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644)

	// Monitor process termination in a separate goroutine.
	// Close the log handle after wait; do not treat Wait failure as fatal.
	go func() {
		_ = cmd.Wait()
		if logFile != nil {
			logFile.Close()
		}
		_ = os.Remove(pidPath)
		dm.mu.Lock()
		if dm.cmd == cmd {
			dm.cmd = nil
		}
		dm.mu.Unlock()
	}()

	// Ensure the process has started and registered in the OS
	started := false
	for i := 0; i < 15; i++ {
		if isPidActive(cmd.Process.Pid) {
			started = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !started {
		return errors.New("daemon process failed to start (check logs/config)")
	}

	return nil
}

// Stop sends a termination/interrupt signal to the daemon and ensures it terminates.
func (dm *DaemonManager) Stop() error {
	dm.mu.Lock()
	cmd := dm.cmd
	dm.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		if dm.config != nil && dm.config.DataDir != "" {
			pidPath := filepath.Join(dm.config.DataDir, "daemon.pid")
			data, err := os.ReadFile(pidPath)
			if err == nil {
				var pid int
				if _, err := fmt.Sscanf(string(data), "%d", &pid); err == nil {
					_ = killPid(pid)
					_ = os.Remove(pidPath)
				}
			}
		}
		_ = killProcess("membuss")
		_ = killProcess("membuss-cli")
		return nil
	}

	// Try graceful stop first
	var err error
	if runtime.GOOS == "windows" {
		err = cmd.Process.Kill()
	} else {
		err = cmd.Process.Signal(os.Interrupt)
	}

	if err == nil {
		// Wait for the process to exit (up to 3 seconds)
		done := make(chan error, 1)
		go func() {
			done <- cmd.Wait()
		}()

		select {
		case <-done:
			// Process exited cleanly
		case <-time.After(3 * time.Second):
			// Timed out, force kill
			_ = cmd.Process.Kill()
			// Wait for the kill to register
			<-done
		}
	} else {
		// Graceful signal failed, try force kill directly
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}

	dm.mu.Lock()
	if dm.cmd == cmd {
		dm.cmd = nil
	}
	dm.mu.Unlock()

	// Clean up any remaining/orphan processes just to be completely safe
	_ = killProcess("membuss")
	_ = killProcess("membuss-cli")
	
	// Ensure the process is actually gone
	for i := 0; i < 15; i++ {
		if !dm.IsRunning() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Wait a tiny bit for the OS to release the socket bindings
	time.Sleep(500 * time.Millisecond)

	return nil
}

func (dm *DaemonManager) resolveBlockedPorts(dataDir string) error {
	// First, load config.yaml if it exists to get the configured daemon ports
	yamlConfig, err := LoadYamlConfig(dataDir)
	if err != nil {
		return err
	}

	// Helper to get string from map
	getAddr := func(key string, defaultVal string) string {
		if val, ok := yamlConfig[key].(string); ok && val != "" {
			return val
		}
		return defaultVal
	}

	// Daemon ports (from config.yaml or defaults)
	daemonGRPC := getAddr("grpc_addr", "127.0.0.1:50051")
	daemonAPI := getAddr("api_addr", "127.0.0.1:5001")
	daemonGateway := getAddr("gateway_addr", "127.0.0.1:8080")

	// Desktop config ports
	desktopGRPC := dm.config.GRPCAddr
	desktopAPI := dm.config.APIAddr
	desktopGateway := dm.config.GatewayAddr

	// Reconcile/sync them: if they differ, prioritize the desktop config
	if desktopGRPC == "" {
		desktopGRPC = daemonGRPC
	}
	if desktopAPI == "" {
		desktopAPI = daemonAPI
	}
	if desktopGateway == "" {
		desktopGateway = daemonGateway
	}

	changed := (desktopGRPC != daemonGRPC || desktopAPI != daemonAPI || desktopGateway != daemonGateway)

	// GRPC
	resolvedGRPC, err := findNextFreePort(desktopGRPC)
	if err == nil && resolvedGRPC != desktopGRPC {
		desktopGRPC = resolvedGRPC
		changed = true
	}

	// API
	resolvedAPI, err := findNextFreePort(desktopAPI)
	if err == nil && resolvedAPI != desktopAPI {
		desktopAPI = resolvedAPI
		changed = true
	}

	// Gateway
	resolvedGateway, err := findNextFreePort(desktopGateway)
	if err == nil && resolvedGateway != desktopGateway {
		desktopGateway = resolvedGateway
		changed = true
	}

	if changed {
		// Update desktop config
		dm.config.GRPCAddr = desktopGRPC
		dm.config.APIAddr = desktopAPI
		dm.config.GatewayAddr = desktopGateway
		_ = dm.config.Save()

		// Update config.yaml
		yamlConfig["grpc_addr"] = desktopGRPC
		yamlConfig["api_addr"] = desktopAPI
		yamlConfig["gateway_addr"] = desktopGateway
		_ = SaveYamlConfig(dataDir, yamlConfig)
	}

	return nil
}

func isPortFree(addr string) bool {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

func findNextFreePort(addr string) (string, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, err
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		return addr, err
	}

	for i := 0; i < 100; i++ {
		candidateAddr := net.JoinHostPort(host, fmt.Sprintf("%d", port+i))
		if isPortFree(candidateAddr) {
			return candidateAddr, nil
		}
	}
	return addr, fmt.Errorf("failed to find free port starting from %s", addr)
}

// DownloadLatestRelease queries GitHub releases and downloads the appropriate zip file.
// If the latest release has no compatible asset compiled yet (common in local dev),
// it will fall back to checking if the binaries are compiled in the local bin/ folder
// and copying them to targetDir to simulate a download.
func (dm *DaemonManager) DownloadLatestRelease(targetDir string, progressCb func(percent int, msg string)) (string, error) {
	progressCb(5, "Checking GitHub for latest releases...")

	client := &http.Client{Timeout: 15 * time.Second}

	var downloadUrl string
	var versionTag string
	var releaseErr error

	info, err := fetchLatestRelease()
	if err != nil {
		releaseErr = err
		progressCb(10, "GitHub API unavailable; will try local binaries if needed...")
	} else {
		versionTag = info.TagName
		downloadUrl = findPlatformAssetURL(info)
		if !info.FromAPI {
			progressCb(12, "Resolved latest tag via release page (API rate-limit fallback)...")
		}
	}

	binDir := filepath.Join(targetDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return "", err
	}

	// Fallback implementation: If download URL is empty, we look for locally built binaries.
	if downloadUrl == "" {
		progressCb(30, "No compatible asset found in GitHub release. Falling back to local binaries...")
		time.Sleep(500 * time.Millisecond)

		rootBin, err := findLocalBinaries()
		if err != nil {
			if releaseErr != nil {
				return "", fmt.Errorf("GitHub release query failed: %w (and local fallback failed: %v)", releaseErr, err)
			}
			return "", fmt.Errorf("no compatible asset found in GitHub release (and local fallback failed: %w)", err)
		}

		exeExt := ""
		if runtime.GOOS == "windows" {
			exeExt = ".exe"
		}
		// Single unified binary: membuss is both node and CLI.
		daemonSrc := filepath.Join(rootBin, "membuss"+exeExt)

		progressCb(60, "Copying local development binary...")

		err = copyFile(daemonSrc, filepath.Join(binDir, "membuss"+exeExt))
		if err != nil {
			return "", fmt.Errorf("failed to copy local membuss binary: %w", err)
		}

		// Also check and update desktop binary if available locally
		desktopSrc := filepath.Join(rootBin, "membuss-desktop"+exeExt)
		if fi, err := os.Stat(desktopSrc); err == nil && !fi.IsDir() {
			progressCb(80, "Applying desktop application self-update...")
			sum := NewSelfUpdateManager()
			if updateErr := sum.ApplySelfUpdate(desktopSrc); updateErr != nil {
				slog.Warn("desktop self-update failed", "error", updateErr)
			}
		}

		progressCb(100, "Installation complete!")
		return version.Version, nil
	}

	// If downloadUrl is found, perform the actual download
	progressCb(20, fmt.Sprintf("Downloading %s release...", versionTag))
	archiveExt := ".zip"
	if runtime.GOOS != "windows" {
		archiveExt = ".tar.gz"
	}
	archivePath := filepath.Join(targetDir, "membuss-latest"+archiveExt)
	
	out, err := os.Create(archivePath)
	if err != nil {
		return "", err
	}
	defer out.Close()

	dresp, err := client.Get(downloadUrl)
	if err != nil {
		return "", err
	}
	defer dresp.Body.Close()

	if dresp.StatusCode != 200 {
		return "", fmt.Errorf("download failed: status %s", dresp.Status)
	}

	// Copy and report progress
	totalSize := dresp.ContentLength
	var downloaded int64
	buf := make([]byte, 32*1024)
	
	for {
		n, rerr := dresp.Body.Read(buf)
		if n > 0 {
			_, werr := out.Write(buf[:n])
			if werr != nil {
				return "", werr
			}
			downloaded += int64(n)
			if totalSize > 0 {
				percent := 20 + int(float64(downloaded)/float64(totalSize)*55.0) // Scale to 20-75% progress
				progressCb(percent, fmt.Sprintf("Downloading... (%d%%)", percent))
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", rerr
		}
	}
	out.Close()

	progressCb(80, "Extracting release binaries...")
	var extractErr error
	if strings.HasSuffix(downloadUrl, ".zip") {
		extractErr = extractZip(archivePath, binDir)
	} else if strings.HasSuffix(downloadUrl, ".tar.gz") {
		extractErr = extractTarGz(archivePath, binDir)
	} else {
		extractErr = fmt.Errorf("unsupported archive format: %s", downloadUrl)
	}
	if extractErr != nil {
		return "", fmt.Errorf("failed to extract archive: %w", extractErr)
	}

	// Check if the extracted archive contained a new desktop application binary
	exeExt := ""
	if runtime.GOOS == "windows" {
		exeExt = ".exe"
	}
	desktopCandidate := filepath.Join(binDir, "membuss-desktop"+exeExt)
	if fi, err := os.Stat(desktopCandidate); err == nil && !fi.IsDir() {
		progressCb(90, "Applying desktop application self-update...")
		sum := NewSelfUpdateManager()
		if updateErr := sum.ApplySelfUpdate(desktopCandidate); updateErr != nil {
			slog.Warn("desktop self-update failed", "error", updateErr)
		}
	}

	progressCb(95, "Cleaning up downloaded archive...")
	_ = os.Remove(archivePath)

	progressCb(100, "Installation complete!")
	return versionTag, nil
}

// findLocalBinaries attempts to dynamically locate the directory containing
// the built membuss binary (the single unified node+CLI executable).
func findLocalBinaries() (string, error) {
	exeExt := ""
	if runtime.GOOS == "windows" {
		exeExt = ".exe"
	}

	// 1. Try relative to the currently running executable path
	if execPath, err := os.Executable(); err == nil {
		execDir := filepath.Dir(execPath)
		candidates := []string{
			execDir,
			filepath.Join(execDir, "bin"),
			filepath.Clean(filepath.Join(execDir, "..")),
			filepath.Clean(filepath.Join(execDir, "..", "bin")),
			filepath.Clean(filepath.Join(execDir, "..", "..")),
			filepath.Clean(filepath.Join(execDir, "..", "..", "bin")),
			filepath.Clean(filepath.Join(execDir, "..", "..", "..", "bin")),
		}

		for _, cand := range candidates {
			daemonPath := filepath.Join(cand, "membuss"+exeExt)
			if fi1, err1 := os.Stat(daemonPath); err1 == nil && !fi1.IsDir() {
				return cand, nil
			}
		}
	}

	// 2. Try relative to the current working directory
	if wd, err := os.Getwd(); err == nil {
		candidates := []string{
			wd,
			filepath.Join(wd, "bin"),
			filepath.Clean(filepath.Join(wd, "..")),
			filepath.Clean(filepath.Join(wd, "..", "bin")),
			filepath.Clean(filepath.Join(wd, "..", "..")),
			filepath.Clean(filepath.Join(wd, "..", "..", "bin")),
		}
		for _, cand := range candidates {
			daemonPath := filepath.Join(cand, "membuss"+exeExt)
			if fi1, err1 := os.Stat(daemonPath); err1 == nil && !fi1.IsDir() {
				return cand, nil
			}
		}
	}

	// 3. Try finding it in system PATH
	if p1, err1 := exec.LookPath("membuss"); err1 == nil {
		return filepath.Dir(p1), nil
	}

	return "", fmt.Errorf("local development binaries not found")
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	
	// Ensure executable permissions
	return os.Chmod(dst, 0755)
}

// extractZip extracts all zip files to dest directory.
func extractZip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		fpath := filepath.Join(dest, f.Name)

		// Check for Zip Slip vulnerability
		if !strings.HasPrefix(fpath, filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path in zip: %s", fpath)
		}

		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(fpath, os.ModePerm)
			continue
		}

		if err = os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return err
		}

		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}

		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()

		if err != nil {
			return err
		}

		// Ensure executable permissions on Unix-like OSes
		if runtime.GOOS != "windows" {
			_ = os.Chmod(fpath, 0755)
		}
	}
	return nil
}

// CheckStatus pings the HTTP API and Gateway endpoints to gather rich, live node telemetry.
func (dm *DaemonManager) CheckStatus(apiAddr, gatewayAddr string) (map[string]any, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://%s/api/v1/node/info", apiAddr))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status code %s", resp.Status)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	info := make(map[string]any)
	// Unwrap {"status": "ok", "data": {...}} or flat map
	if dataMap, ok := payload["data"].(map[string]any); ok {
		for k, v := range dataMap {
			info[k] = v
		}
	} else {
		for k, v := range payload {
			info[k] = v
		}
	}

	// Also check peers count from API
	if pResp, pErr := client.Get(fmt.Sprintf("http://%s/api/v1/peers", apiAddr)); pErr == nil {
		defer pResp.Body.Close()
		var pPayload map[string]any
		if err := json.NewDecoder(pResp.Body).Decode(&pPayload); err == nil {
			if pData, ok := pPayload["data"].([]any); ok {
				info["num_peers"] = len(pData)
			} else if pList, ok := pPayload["peers"].([]any); ok {
				info["num_peers"] = len(pList)
			}
		}
	}

	// Query rich telemetry from Gateway if available
	if gatewayAddr != "" {
		req, _ := http.NewRequest("GET", fmt.Sprintf("http://%s/?format=json", gatewayAddr), nil)
		req.Header.Set("Accept", "application/json")
		if gResp, gErr := client.Do(req); gErr == nil {
			defer gResp.Body.Close()
			var gData map[string]any
			if err := json.NewDecoder(gResp.Body).Decode(&gData); err == nil {
				if v, exists := gData["StoreBytes"]; exists {
					info["repo_size"] = v
				}
				if v, exists := gData["BlockCount"]; exists {
					info["num_blocks"] = v
				}
				if v, exists := gData["SealedCount"]; exists {
					info["sealed_count"] = v
				}
				if v, exists := gData["PeerCount"]; exists {
					info["num_peers"] = v
				}
				if v, exists := gData["Uptime"]; exists {
					info["uptime_sec"] = v
				}
				if v, exists := gData["SealedList"]; exists {
					info["sealed_list"] = v
				}
				if v, exists := gData["AllFiles"]; exists {
					info["all_files"] = v
				}
				if nodeInfo, ok := gData["NodeInfo"].(map[string]any); ok {
					if isAnchor, ok := nodeInfo["AnchorMode"].(bool); ok {
						info["is_anchor"] = isAnchor
					}
					if peerID, ok := nodeInfo["PeerID"].(string); ok && peerID != "" {
						info["peer_id"] = peerID
					}
					if addrs, ok := nodeInfo["Addrs"].([]any); ok && len(addrs) > 0 {
						info["addrs"] = addrs
					}
				}
			}
		}
	}

	return info, nil
}

// CheckExplorer checks if the gateway's explorer is online.
func (dm *DaemonManager) CheckExplorer(gatewayAddr string) bool {
	conn, err := net.DialTimeout("tcp", gatewayAddr, 1*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// LoadYamlConfig reads config.yaml from target dir.
func LoadYamlConfig(dataDir string) (map[string]any, error) {
	path := filepath.Join(dataDir, "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]any), nil
		}
		return nil, err
	}
	var out map[string]any
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SaveYamlConfig serializes and writes config.yaml.
func SaveYamlConfig(dataDir string, cfg map[string]any) error {
	path := filepath.Join(dataDir, "config.yaml")
	_ = os.MkdirAll(dataDir, 0755)
	
	// Set default data_dir in config.yaml to target directory
	cfg["data_dir"] = filepath.ToSlash(dataDir)
	if geo, ok := cfg["geolocation_db"].(string); ok {
		cfg["geolocation_db"] = filepath.ToSlash(geo)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// WriteDefaultConfig generates a complete config.yaml with all fields
// from config.Default(), overriding data_dir with the target directory.
func WriteDefaultConfig(dataDir string) error {
	cfg := config.Default()
	cfg.DataDir = filepath.ToSlash(dataDir)

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	path := filepath.Join(dataDir, "config.yaml")
	_ = os.MkdirAll(dataDir, 0755)
	return os.WriteFile(path, data, 0644)
}

// extractTarGz extracts all files from a tar.gz archive to dest directory.
func extractTarGz(src, dest string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// The files in the archive are at the root (membuss, membuss-cli)
		// Clean and join paths safely
		fpath := filepath.Join(dest, header.Name)

		// Check for Zip Slip / Path Traversal vulnerability
		if !strings.HasPrefix(fpath, filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("tar: illegal file path: %s", fpath)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(fpath, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(fpath), 0755); err != nil {
				return err
			}
			
			outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, header.FileInfo().Mode())
			if err != nil {
				return err
			}
			
			if _, err := io.Copy(outFile, tr); err != nil {
				outFile.Close()
				return err
			}
			outFile.Close()

			// Ensure executable permissions on Unix-like OSes
			if runtime.GOOS != "windows" {
				_ = os.Chmod(fpath, 0755)
			}
		}
	}
	return nil
}
