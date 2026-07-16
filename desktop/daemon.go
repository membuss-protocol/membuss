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

// DaemonManager manages the background daemon process.
type DaemonManager struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	config *DesktopConfig
	ctx    context.Context
}

func NewDaemonManager(cfg *DesktopConfig) *DaemonManager {
	return &DaemonManager{
		config: cfg,
	}
}

// IsRunning checks if the daemon command is active locally.
func (dm *DaemonManager) IsRunning() bool {
	dm.mu.Lock()
	cmd := dm.cmd
	dm.mu.Unlock()
	if cmd != nil && cmd.Process != nil && cmd.ProcessState == nil {
		return true
	}
	if dm.config != nil && dm.config.DataDir != "" {
		pidPath := filepath.Join(dm.config.DataDir, "daemon.pid")
		data, err := os.ReadFile(pidPath)
		if err == nil {
			var pid int
			if _, err := fmt.Sscanf(string(data), "%d", &pid); err == nil {
				if isPidActive(pid) {
					return true
				}
			}
		}
	}
	return isProcessRunning("membuss")
}

// Start spawns the daemon in the background.
func (dm *DaemonManager) Start(dataDir string) error {
	// 1. Resolve any blocked ports dynamically
	if err := dm.resolveBlockedPorts(dataDir); err != nil {
		// Log error but continue trying to start
	}

	if dm.IsRunning() {
		return errors.New("daemon is already running")
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

	// Create a log file inside the data directory
	logDir := filepath.Join(dataDir, "logs")
	_ = os.MkdirAll(logDir, 0755)
	logFile, err := os.OpenFile(filepath.Join(logDir, "daemon.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		logFile = nil
	}

	startCmd := func(detached bool) (*exec.Cmd, error) {
		c := exec.Command(binaryPath, "-datadir", dataDir, "-config", configPath)
		if detached {
			hideConsoleWindow(c)
		} else {
			hideConsoleWindowSimple(c)
		}
		if logFile != nil {
			c.Stdout = logFile
			c.Stderr = logFile
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
		daemonSrc := filepath.Join(rootBin, "membuss"+exeExt)
		cliSrc := filepath.Join(rootBin, "membuss-cli"+exeExt)

		progressCb(60, "Copying local development binaries...")
		
		err = copyFile(daemonSrc, filepath.Join(binDir, "membuss"+exeExt))
		if err != nil {
			return "", fmt.Errorf("failed to copy local daemon binary: %w", err)
		}
		
		err = copyFile(cliSrc, filepath.Join(binDir, "membuss-cli"+exeExt))
		if err != nil {
			return "", fmt.Errorf("failed to copy local CLI binary: %w", err)
		}

		progressCb(100, "Local installation complete!")
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
				percent := 20 + int(float64(downloaded)/float64(totalSize)*60.0) // Scale to 20-80% progress
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

	progressCb(85, "Extracting binaries...")
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

	progressCb(95, "Cleaning up downloaded archive...")
	_ = os.Remove(archivePath)

	progressCb(100, "Installation complete!")
	return versionTag, nil
}

// findLocalBinaries attempts to dynamically locate the directory containing
// the built membuss and membuss-cli binaries.
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
			cliPath := filepath.Join(cand, "membuss-cli"+exeExt)
			if fi1, err1 := os.Stat(daemonPath); err1 == nil && !fi1.IsDir() {
				if fi2, err2 := os.Stat(cliPath); err2 == nil && !fi2.IsDir() {
					return cand, nil
				}
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
			cliPath := filepath.Join(cand, "membuss-cli"+exeExt)
			if fi1, err1 := os.Stat(daemonPath); err1 == nil && !fi1.IsDir() {
				if fi2, err2 := os.Stat(cliPath); err2 == nil && !fi2.IsDir() {
					return cand, nil
				}
			}
		}
	}

	// 3. Try finding them in system PATH
	if p1, err1 := exec.LookPath("membuss"); err1 == nil {
		if p2, err2 := exec.LookPath("membuss-cli"); err2 == nil {
			dDir := filepath.Dir(p1)
			cDir := filepath.Dir(p2)
			if dDir == cDir {
				return dDir, nil
			}
		}
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

// CheckStatus pings the HTTP API endpoint to verify node health.
func (dm *DaemonManager) CheckStatus(apiAddr string) (map[string]any, error) {
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

	return payload, nil
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
