package memgate_v2

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetrics_DefaultRestrictedAccess(t *testing.T) {
	mb := newMemBackend()
	mg, err := New(Config{Backend: mb})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// 1. Non-localhost IP request to /metrics without token should be blocked (403 Forbidden)
	req := httptest.NewRequest("GET", "/metrics", nil)
	req.Host = "gateway.localhost"
	req.RemoteAddr = "198.51.100.4:54321" // External IP
	rec := httptest.NewRecorder()

	mg.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for public unauthenticated /metrics, got %d, body: %s", rec.Code, rec.Body.String())
	}

	// 2. Localhost IP request to /metrics should succeed (200 OK)
	localReq := httptest.NewRequest("GET", "/metrics", nil)
	localReq.Host = "localhost"
	localReq.RemoteAddr = "127.0.0.1:12345"
	localRec := httptest.NewRecorder()

	mg.Router().ServeHTTP(localRec, localReq)

	if localRec.Code != http.StatusOK {
		t.Errorf("expected 200 OK for local /metrics, got %d, body: %s", localRec.Code, localRec.Body.String())
	}
	body := localRec.Body.String()
	if !strings.Contains(body, "membuss_gateway_requests_total") {
		t.Errorf("expected metric membuss_gateway_requests_total in output, got:\n%s", body)
	}
}

func TestMetrics_BearerTokenAuthentication(t *testing.T) {
	mb := newMemBackend()
	secret := "secret-metrics-token-123"
	mg, err := New(Config{
		Backend:      mb,
		MetricsToken: secret,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// 1. Public request without token -> 401 Unauthorized
	req1 := httptest.NewRequest("GET", "/metrics", nil)
	req1.Host = "gateway.localhost"
	req1.RemoteAddr = "198.51.100.4:54321"
	rec1 := httptest.NewRecorder()

	mg.Router().ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized without token, got %d", rec1.Code)
	}

	// 2. Public request with valid Authorization header -> 200 OK
	req2 := httptest.NewRequest("GET", "/metrics", nil)
	req2.Host = "gateway.localhost"
	req2.RemoteAddr = "198.51.100.4:54321"
	req2.Header.Set("Authorization", "Bearer "+secret)
	rec2 := httptest.NewRecorder()

	mg.Router().ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Errorf("expected 200 OK with valid bearer token, got %d, body: %s", rec2.Code, rec2.Body.String())
	}

	// 3. Public request with valid query param ?token=secret -> 200 OK
	req3 := httptest.NewRequest("GET", "/explorer/metrics?token="+secret, nil)
	req3.Host = "gateway.localhost"
	req3.RemoteAddr = "198.51.100.4:54321"
	rec3 := httptest.NewRecorder()

	mg.Router().ServeHTTP(rec3, req3)

	if rec3.Code != http.StatusOK {
		t.Errorf("expected 200 OK with valid query token, got %d, body: %s", rec3.Code, rec3.Body.String())
	}
}

func TestMetrics_IncrementsCountersOnRequestsAndCache(t *testing.T) {
	mb := newMemBackend()
	data := []byte("metrics payload test")
	m := putRandom(mb, data)

	mg, err := New(Config{Backend: mb})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	srv := httptest.NewServer(mg.Router())
	defer srv.Close()

	// 1. Fetch MID twice to trigger 1 cache miss and 1 cache hit
	resp1, err := http.Get(srv.URL + "/mem/" + m.String())
	if err != nil {
		t.Fatalf("get 1: %v", err)
	}
	_, _ = io.ReadAll(resp1.Body)
	resp1.Body.Close()

	resp2, err := http.Get(srv.URL + "/mem/" + m.String())
	if err != nil {
		t.Fatalf("get 2: %v", err)
	}
	_, _ = io.ReadAll(resp2.Body)
	resp2.Body.Close()

	// 2. Query /metrics from localhost
	mReq, _ := http.NewRequest("GET", srv.URL+"/metrics", nil)
	mRec, err := http.DefaultClient.Do(mReq)
	if err != nil {
		t.Fatalf("get metrics: %v", err)
	}
	defer mRec.Body.Close()

	mBody, _ := io.ReadAll(mRec.Body)
	out := string(mBody)

	if !strings.Contains(out, "membuss_gateway_requests_total") {
		t.Errorf("missing membuss_gateway_requests_total in output:\n%s", out)
	}
	if !strings.Contains(out, "membuss_gateway_cache_hits_total 1") {
		t.Errorf("expected cache hits total 1, got output:\n%s", out)
	}
	if !strings.Contains(out, "membuss_gateway_cache_misses_total 1") {
		t.Errorf("expected cache misses total 1, got output:\n%s", out)
	}
}
