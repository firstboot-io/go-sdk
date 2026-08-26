package firstboot

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/firstboot-io/firstboot-go/fbapi"
)

// These drive the transports against a fake API rather than a real one. What
// they pin is not "the HTTP call works" -- the generated client handles that --
// but the three behaviours this package adds and would otherwise get wrong
// silently: the key that survives a retry, the header that is honoured, and the
// refusal that is not retried.

func newTestClient(t *testing.T, h http.Handler, opts ...Option) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	all := append([]Option{WithBaseURL(srv.URL), WithToken("pat_" + strings.Repeat("a", 40))}, opts...)
	c, err := New(all...)
	if err != nil {
		t.Fatal(err)
	}
	return c, srv
}

// The property the whole retry design rests on: a retried create carries the
// SAME key, so the API answers it with the first call's resource instead of
// creating a second one. A key minted per attempt would silently buy two
// servers, and nothing about the client's behaviour would look wrong.
func TestRetryReusesOneIdempotencyKey(t *testing.T) {
	var mu sync.Mutex
	var keys []string

	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		keys = append(keys, r.Header.Get(IdempotencyHeader))
		n := len(keys)
		mu.Unlock()
		if n < 3 {
			// The failure this exists for: the work may well have happened and
			// only the answer was lost.
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"server":{"id":"11111111-1111-1111-1111-111111111111","code":"s-1","name":"web-1","state":"provisioning","plan":{},"monthly_charge_minor":0,"charge_currency":"TRY","image":{},"billing_mode":"monthly","state_since":"2026-08-24T00:00:00Z","created_at":"2026-08-24T00:00:00Z"}}`))
	}), WithRetry(RetryPolicy{MaxAttempts: 4, Base: time.Millisecond, Max: 2 * time.Millisecond, MaxRetryAfter: time.Second}))

	_, err := c.API.ServerCreateWithBody(context.Background(), &fbapi.ServerCreateParams{},
		"application/json", strings.NewReader(`{"name":"web-1","plan":"s1"}`))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(keys) != 3 {
		t.Fatalf("expected 3 attempts, got %d", len(keys))
	}
	if keys[0] == "" {
		t.Fatal("the client did not set an Idempotency-Key at all")
	}
	for i, k := range keys {
		if k != keys[0] {
			t.Fatalf("attempt %d carried a different key (%q vs %q): a retry would create a second server", i+1, k, keys[0])
		}
	}
}

// A caller's own key must survive. The auto-generated one is a convenience; a
// key derived from something stable outside this process is the real thing, and
// overwriting it would break exactly the caller who understood the problem.
func TestCallerKeyIsNotOverwritten(t *testing.T) {
	var got string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get(IdempotencyHeader)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{}`))
	}))

	_, err := c.API.ServerCreateWithBody(context.Background(), &fbapi.ServerCreateParams{}, "application/json",
		strings.NewReader(`{}`), WithIdempotencyKey("tf:module.web.firstboot_server.this"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "tf:module.web.firstboot_server.this" {
		t.Fatalf("the caller's key was replaced with %q", got)
	}
}

// Retry-After is the platform telling the client exactly how long a slot takes
// to free. Ignoring it in favour of a local backoff is how a client either
// hammers a limit or waits far longer than it needed to.
func TestRetryAfterIsHonoured(t *testing.T) {
	var mu sync.Mutex
	var times []time.Time

	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		times = append(times, time.Now())
		n := len(times)
		mu.Unlock()
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"detail":"CREATE_COOLDOWN"}`))
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{}`))
	}), WithRetry(RetryPolicy{MaxAttempts: 2, Base: time.Millisecond, Max: time.Millisecond, MaxRetryAfter: 5 * time.Second}))

	_, err := c.API.ServerCreateWithBody(context.Background(), &fbapi.ServerCreateParams{}, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(times) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(times))
	}
	// The local backoff here is 1ms, so anything close to a second can only
	// have come from the header.
	if gap := times[1].Sub(times[0]); gap < 900*time.Millisecond {
		t.Fatalf("Retry-After was ignored: waited %s, the header asked for 1s", gap)
	}
}

// A refusal a retry cannot change must not be retried. IDEMPOTENCY_KEY_REUSED
// is the sharpest case: the caller is sending one key for two different
// requests, and hammering it neither succeeds nor tells them what is wrong.
func TestUnretryableRefusalIsNotRetried(t *testing.T) {
	var attempts int
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"title":"Unprocessable Entity","status":422,"detail":"IDEMPOTENCY_KEY_REUSED"}`))
	}), WithRetry(RetryPolicy{MaxAttempts: 4, Base: time.Millisecond, Max: time.Millisecond}))

	resp, err := c.API.ServerCreateWithBody(context.Background(), &fbapi.ServerCreateParams{}, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if attempts != 1 {
		t.Fatalf("a 422 was retried %d times; it can never succeed as sent", attempts)
	}
}

// New refuses rather than defaulting, because both failures otherwise surface
// far from their cause: no URL becomes a connection error, no token becomes a
// 401 that reads as a permissions problem.
func TestNewRefusesAnUnusableConfiguration(t *testing.T) {
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvToken, "")
	if _, err := New(); err == nil {
		t.Fatal("New must refuse with no base URL")
	}
	if _, err := New(WithBaseURL("https://api.example.com")); err == nil {
		t.Fatal("New must refuse with no token")
	}
	if _, err := New(WithBaseURL("https://api.example.com"), WithToken("not-a-token")); err == nil {
		t.Fatal("New must refuse a token that is not shaped like one")
	}
	if _, err := New(WithBaseURL("https://api.example.com"), WithToken("pat_"+strings.Repeat("a", 40))); err != nil {
		t.Fatalf("a well-formed configuration was refused: %v", err)
	}
}

func TestEnvironmentIsRead(t *testing.T) {
	t.Setenv(EnvBaseURL, "https://api.example.com")
	t.Setenv(EnvToken, "pat_"+strings.Repeat("b", 40))
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if c.baseURL != "https://api.example.com" {
		t.Fatalf("base URL = %q", c.baseURL)
	}
	// An explicit option still wins, which is what makes the environment a
	// default rather than a rule.
	c2, err := New(WithBaseURL("https://other.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if c2.baseURL != "https://other.example.com" {
		t.Fatalf("WithBaseURL did not override the environment: %q", c2.baseURL)
	}
}

func TestWaiterStopsOnAFailureState(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"11111111-1111-1111-1111-111111111111","code":"s-1","name":"web-1","state":"error_provisioning","error_code":"NO_TEMPLATE","plan":{},"monthly_charge_minor":0,"charge_currency":"TRY","image":{},"billing_mode":"monthly","state_since":"2026-08-24T00:00:00Z","created_at":"2026-08-24T00:00:00Z"}`))
	}))

	_, err := c.WaitForServer(context.Background(), "s-1", WithTimeout(2*time.Second))
	var se *StateError
	if !errors.As(err, &se) {
		t.Fatalf("expected a *StateError, got %v", err)
	}
	if se.State != "error_provisioning" || se.Code != "NO_TEMPLATE" {
		t.Fatalf("the failure lost its detail: %+v", se)
	}
}

// A server that settles `stopped` is a finished, billable machine. Calling that
// an error would be a product judgement this library has no standing to make.
func TestWaiterDoesNotCallStoppedAFailure(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"11111111-1111-1111-1111-111111111111","code":"s-1","name":"web-1","state":"stopped","plan":{},"monthly_charge_minor":0,"charge_currency":"TRY","image":{},"billing_mode":"monthly","state_since":"2026-08-24T00:00:00Z","created_at":"2026-08-24T00:00:00Z"}`))
	}))
	srv, err := c.WaitForServer(context.Background(), "s-1", WithTimeout(2*time.Second))
	if err != nil {
		t.Fatalf("a stopped server is not a failure: %v", err)
	}
	if srv == nil || string(srv.State) != "stopped" {
		t.Fatal("the waiter did not return the server it settled on")
	}
}
