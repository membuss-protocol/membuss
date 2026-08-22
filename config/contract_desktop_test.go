// Desktop submodule contract (finding.txt XC-005).
//
// desktop/ imports this package through `replace => ../` and does
// exactly two things with it:
//  1. WriteDefaultConfig: config.Default() -> yaml.Marshal -> write config.yaml
//  2. links core/version.Version
//
// This test pins that surface. If a change here breaks the desktop
// build or the config files it writes, this fails first — in the
// root module, where CI runs it — instead of silently breaking the
// desktop release build.
package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nnlgsakib/membuss/core/version"
	"gopkg.in/yaml.v3"
)

// TestDesktopContractDefaultRoundTrip mirrors desktop WriteDefaultConfig:
// Default() -> yaml.Marshal -> file -> Load must preserve the fields the
// daemon needs to boot (addresses, data dir).
func TestDesktopContractDefaultRoundTrip(t *testing.T) {
	cfg := Default()
	if cfg == nil {
		t.Fatal("config.Default() returned nil")
	}
	if cfg.APIAddr == "" || cfg.GatewayAddr == "" || cfg.GRPCAddr == "" {
		t.Fatalf("Default() missing listen addrs: api=%q gw=%q grpc=%q",
			cfg.APIAddr, cfg.GatewayAddr, cfg.GRPCAddr)
	}
	cfg.DataDir = filepath.ToSlash(filepath.Join(t.TempDir(), "node"))

	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("yaml.Marshal(Default()): %v", err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatalf("Load(marshaled default): %v", err)
	}
	checks := []struct {
		name      string
		got, want string
	}{
		{"DataDir", back.DataDir, cfg.DataDir},
		{"APIAddr", back.APIAddr, "127.0.0.1:5001"},
		{"GatewayAddr", back.GatewayAddr, "127.0.0.1:8080"},
		{"GRPCAddr", back.GRPCAddr, "127.0.0.1:50051"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q (round-trip drift)", c.name, c.got, c.want)
		}
	}
}

// TestDesktopContractVersionSymbol pins the version variable the desktop
// UI displays and update-checks against.
func TestDesktopContractVersionSymbol(t *testing.T) {
	if version.Version == "" {
		t.Fatal("core/version.Version is empty")
	}
}
