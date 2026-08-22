package explorer

import (
	"log/slog"
	"net"
	"net/http"
)

// audit records a destructive admin operation in the node's audit
// trail (finding.txt XC-008). Best-effort: failures are logged, never
// surfaced to the requester.
func (e *Explorer) audit(r *http.Request, action, target string, detail map[string]string) {
	if e.cfg.Audit == nil {
		return
	}
	actor, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || actor == "" {
		actor = r.RemoteAddr
	}
	if err := e.cfg.Audit.Log(actor, action, target, detail); err != nil {
		slog.Error("explorer: audit write failed", "action", action, "err", err)
	}
}
