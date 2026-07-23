package plugin_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nnlgsakib/membuss/config"
	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"
	"github.com/nnlgsakib/membuss/pkg/plugin"
	_ "github.com/nnlgsakib/membuss/plugins"
)

func TestPluginFramework_RegistrationAndLifecycle(t *testing.T) {
	registered := plugin.GetRegistered()
	if _, ok := registered["echo-inspector"]; !ok {
		t.Fatalf("expected echo-inspector plugin to be registered")
	}

	cfg := config.Default()
	cfg.Plugins.Enabled = true
	cfg.Plugins.Active = []string{"echo-inspector"}

	hookBus := plugin.NewHookBus()
	gateHTTP := plugin.NewMapHTTPRegistry()
	nodeHTTP := plugin.NewMapHTTPRegistry()
	cliReg := plugin.NewMapCLIRegistry()

	core := &plugin.Core{
		Config:      cfg,
		Hooks:       hookBus,
		GatewayHTTP: gateHTTP,
		NodeHTTP:    nodeHTTP,
		CLIRegistry: cliReg,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	if err := plugin.BootPlugins(core); err != nil {
		t.Fatalf("BootPlugins failed: %v", err)
	}

	ctx := context.Background()
	if err := plugin.StartPlugins(ctx); err != nil {
		t.Fatalf("StartPlugins failed: %v", err)
	}
	defer plugin.StopPlugins(ctx)

	// Verify CLI registration
	cmds := cliReg.GetCommands()
	if _, ok := cmds["inspector"]; !ok {
		t.Errorf("expected inspector CLI command to be registered")
	}

	// Verify HTTP registration
	if _, ok := gateHTTP.Handlers["GET /gateway/inspector/status"]; !ok {
		t.Errorf("expected /gateway/inspector/status route to be registered on gateway")
	}

	req := httptest.NewRequest("GET", "/gateway/inspector/status", nil)
	w := httptest.NewRecorder()
	gateHTTP.Handlers["GET /gateway/inspector/status"].ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200 OK, got %d", w.Code)
	}

	// Verify Hook execution
	raw := []byte("hello plugin system test")
	testMID := mid.FromBytes(raw)

	blk := &store.Block{MID: testMID, Data: raw}
	outBlk, err := hookBus.TriggerBeforeBlockPut(ctx, blk)
	if err != nil {
		t.Fatalf("TriggerBeforeBlockPut failed: %v", err)
	}
	if outBlk == nil {
		t.Fatalf("expected non-nil output block")
	}
}
