package tunnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"

	ma "github.com/multiformats/go-multiaddr"
	"golang.ngrok.com/ngrok"
	ngconfig "golang.ngrok.com/ngrok/config"
	"github.com/nnlgsakib/membuss/config"
)

var (
	globalNgrokAddrMu sync.RWMutex
	globalNgrokAddr   ma.Multiaddr
)

// SetNgrokAddress sets the active ngrok address for announce.
func SetNgrokAddress(addr ma.Multiaddr) {
	globalNgrokAddrMu.Lock()
	defer globalNgrokAddrMu.Unlock()
	globalNgrokAddr = addr
}

// GetNgrokAddress gets the active ngrok address.
func GetNgrokAddress() ma.Multiaddr {
	globalNgrokAddrMu.RLock()
	defer globalNgrokAddrMu.RUnlock()
	return globalNgrokAddr
}

type Manager struct {
	mu          sync.Mutex
	CfgPath     string
	activeTun   ngrok.Tunnel
	cancel      context.CancelFunc
	status      string // "inactive", "connecting", "active", "error"
	lastError   string
	publicAddr  string
}

func NewManager(cfgPath string) *Manager {
	return &Manager{
		CfgPath: cfgPath,
		status:  "inactive",
	}
}

// Status returns the current status of the tunnel.
func (m *Manager) Status() (status, publicAddr, lastError string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status, m.publicAddr, m.lastError
}

// SaveConfig updates and writes the tunnel config to the yaml file.
func (m *Manager) SaveConfig(enabled bool, authtoken string) error {
	// Load config
	cfg, err := config.Load(m.CfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	cfg.Tunnel.Enabled = enabled
	cfg.Tunnel.Authtoken = authtoken

	// Write config
	if err := config.WriteConfig(cfg, m.CfgPath); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

// Start initiates the ngrok tunnel connection if enabled.
func (m *Manager) Start(ctx context.Context, cfg *config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.status == "active" || m.status == "connecting" {
		return nil
	}

	if cfg.Tunnel.Authtoken == "" {
		m.status = "error"
		m.lastError = "authtoken is not configured"
		return fmt.Errorf("authtoken not configured")
	}

	m.status = "connecting"
	m.lastError = ""
	m.publicAddr = ""

	runCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel

	localPort := getLocalTCPPort(cfg.ListenAddrs)
	targetAddr := net.JoinHostPort("127.0.0.1", localPort)

	go func() {
		tun, err := ngrok.Listen(runCtx,
			ngconfig.TCPEndpoint(),
			ngrok.WithAuthtoken(cfg.Tunnel.Authtoken),
		)
		if err != nil {
			m.mu.Lock()
			m.status = "error"
			m.lastError = fmt.Sprintf("ngrok start: %v", err)
			m.mu.Unlock()
			cancel()
			return
		}

		m.mu.Lock()
		m.activeTun = tun
		m.publicAddr = tun.URL()
		m.status = "active"
		m.mu.Unlock()

		maddr, err := urlToMultiaddr(tun.URL())
		if err == nil {
			SetNgrokAddress(maddr)
		}

		forwardLoop(runCtx, tun, targetAddr)

		m.mu.Lock()
		if m.activeTun == tun {
			m.activeTun = nil
			m.status = "inactive"
			m.publicAddr = ""
			SetNgrokAddress(nil)
		}
		m.mu.Unlock()
	}()

	return nil
}

// Stop closes the active ngrok tunnel.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.activeTun != nil {
		_ = m.activeTun.Close()
		m.activeTun = nil
	}
	m.status = "inactive"
	m.publicAddr = ""
	m.lastError = ""
	SetNgrokAddress(nil)
}

func forwardLoop(ctx context.Context, listener net.Listener, targetAddr string) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			localConn, err := net.Dial("tcp", targetAddr)
			if err != nil {
				return
			}
			defer localConn.Close()

			errChan := make(chan error, 2)
			go func() {
				_, err := io.Copy(localConn, c)
				errChan <- err
			}()
			go func() {
				_, err := io.Copy(c, localConn)
				errChan <- err
			}()

			select {
			case <-ctx.Done():
			case <-errChan:
			}
		}(conn)
	}
}

func urlToMultiaddr(ngrokURL string) (ma.Multiaddr, error) {
	raw := strings.TrimPrefix(ngrokURL, "tcp://")
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		return nil, err
	}
	return ma.NewMultiaddr(fmt.Sprintf("/dns4/%s/tcp/%s", host, port))
}

func getLocalTCPPort(listenAddrs []string) string {
	for _, addr := range listenAddrs {
		if strings.Contains(addr, "/tcp/") && !strings.Contains(addr, "/ws") {
			parts := strings.Split(addr, "/")
			for i, part := range parts {
				if part == "tcp" && i+1 < len(parts) {
					return parts[i+1]
				}
			}
		}
	}
	return "4001"
}
