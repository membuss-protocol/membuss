package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// countingWriter records how many times WriteHeader is called so a
// superfluous second call (the bug ctxTimeout replaces) is
// observable in a test.
type countingWriter struct {
	*httptest.ResponseRecorder
	writeHeaderCalls int
}

func (c *countingWriter) WriteHeader(status int) {
	c.writeHeaderCalls++
	c.ResponseRecorder.WriteHeader(status)
}

// TestCtxTimeout_PropagatesDeadline verifies the middleware installs
// a deadline on the request context that the handler can observe.
func TestCtxTimeout_PropagatesDeadline(t *testing.T) {
	var gotDeadline bool
	h := ctxTimeout(50 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Context().Deadline(); ok {
			gotDeadline = true
		}
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/x", nil)
	h.ServeHTTP(&countingWriter{ResponseRecorder: httptest.NewRecorder()}, req)
	if !gotDeadline {
		t.Fatal("handler did not observe a context deadline")
	}
}

// TestCtxTimeout_NoDoubleWriteHeader is the regression test for the
// "superfluous response.WriteHeader" log line: a handler whose work
// exceeds the deadline and then writes its own error envelope must
// leave the middleware with nothing to write.
func TestCtxTimeout_NoDoubleWriteHeader(t *testing.T) {
	h := ctxTimeout(10 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate a slow lookup that ends because the request
		// context deadline fired (as a DHT/store lookup would).
		<-r.Context().Done()
		if r.Context().Err() != context.DeadlineExceeded {
			t.Errorf("expected DeadlineExceeded, got %v", r.Context().Err())
		}
		// Handler surfaces its own error, exactly as handleMemNSResolve
		// does — this is the single, authoritative WriteHeader.
		fail(w, http.StatusGatewayTimeout, r.Context().Err())
	}))
	cw := &countingWriter{ResponseRecorder: httptest.NewRecorder()}
	h.ServeHTTP(cw, httptest.NewRequest("GET", "/slow", nil))
	if cw.writeHeaderCalls != 1 {
		t.Fatalf("WriteHeader called %d times, want exactly 1 (double-write regression)", cw.writeHeaderCalls)
	}
}

// TestCtxTimeout_ZeroDisables confirms a non-positive timeout is a
// pass-through and adds no deadline.
func TestCtxTimeout_ZeroDisables(t *testing.T) {
	var hadDeadline bool
	h := ctxTimeout(0)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadDeadline = r.Context().Deadline()
		w.WriteHeader(http.StatusOK)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
	if hadDeadline {
		t.Fatal("zero timeout should not install a deadline")
	}
}
