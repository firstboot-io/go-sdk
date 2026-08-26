package firstboot

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Retrying, and the one rule that makes it safe.
//
// A retry is only ever correct because the request carries an `Idempotency-Key`
// (see idempotency.go). Without one, a retried create opens a second server and
// bills a second month; with one, the API answers the retry with the first
// call's resource. That is why the idempotency transport sits OUTSIDE this one:
// the key is minted once, before the first attempt, and every attempt below
// carries the same header.

// RetryPolicy bounds how hard the client tries. The zero value retries nothing,
// which is what WithRetry(RetryPolicy{}) means.
type RetryPolicy struct {
	// MaxAttempts includes the first one, so 1 means "no retry" and 0 means the
	// same thing said by accident.
	MaxAttempts int
	// Base is the first backoff; each attempt doubles it up to Max.
	Base time.Duration
	Max  time.Duration
	// MaxRetryAfter caps how long the client will honour a server-sent
	// Retry-After. The platform's create cooldown can answer with most of an
	// hour, and a library that sleeps for an hour inside one call has stopped
	// being a library. Past this the error is returned and the caller decides.
	MaxRetryAfter time.Duration
}

// DefaultRetryPolicy is the backoff the customer documentation already
// prescribes for polling (1s to 15s), applied to transport failures.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:   4,
		Base:          time.Second,
		Max:           15 * time.Second,
		MaxRetryAfter: 30 * time.Second,
	}
}

type retryTransport struct {
	next   http.RoundTripper
	policy RetryPolicy
	now    func() time.Time
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	attempts := t.policy.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	// A request whose body cannot be rewound can be sent exactly once. Rather
	// than replaying a consumed reader and sending an empty body -- which the
	// API would answer with a validation error that looks nothing like the real
	// problem -- this degrades to no retry at all.
	if req.Body != nil && req.Body != http.NoBody && req.GetBody == nil {
		attempts = 1
	}

	var lastResp *http.Response
	var lastErr error
	for attempt := 1; ; attempt++ {
		if attempt > 1 {
			if req.GetBody != nil {
				body, err := req.GetBody()
				if err != nil {
					return lastResp, lastErr
				}
				req.Body = body
			}
		}
		resp, err := t.next.RoundTrip(req)
		lastResp, lastErr = resp, err

		if attempt >= attempts {
			return resp, err
		}
		wait, ok := t.shouldRetry(resp, err)
		if !ok {
			return resp, err
		}
		if wait <= 0 {
			wait = t.backoff(attempt)
		}
		// The response body has to be drained and closed or the connection is
		// not reused, and a retry loop that leaks one per attempt is a
		// connection leak under exactly the load that triggers retries.
		if resp != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
			_ = resp.Body.Close()
		}
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(wait):
		}
	}
}

// shouldRetry answers whether to try again and, when the server said how long
// to wait, how long. A false answer is final.
func (t *retryTransport) shouldRetry(resp *http.Response, err error) (time.Duration, bool) {
	if err != nil {
		// A transport error is the case this whole mechanism exists for: the
		// request may well have been executed and only its answer was lost.
		// Retryable BECAUSE the request carries an idempotency key, and only
		// because of it. A cancelled context is the caller's own decision and
		// never ours to override.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return 0, false
		}
		return 0, true
	}
	if resp == nil {
		return 0, false
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		// The platform answers a refused create with Retry-After measured from
		// the moment a slot actually frees, so honouring it is strictly better
		// than any backoff this client could invent.
		if d, ok := retryAfter(resp.Header, t.now()); ok {
			if d > t.policy.MaxRetryAfter {
				return 0, false
			}
			return d, true
		}
		return 0, true
	case http.StatusConflict:
		// Only one 409 is retryable, and it is retryable by construction:
		// IDEMPOTENCY_CONFLICT means another request holding the same key
		// committed between our lookup and our insert, so by the next attempt
		// its row exists and the API replays it. Every other 409 is a state
		// conflict that a retry cannot change.
		return 0, hasCode(resp, "IDEMPOTENCY_CONFLICT")
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return 0, true
	case http.StatusInternalServerError:
		// Retried because this API's 500s are genuinely unexpected states
		// rather than a category it answers with routinely, and because the
		// idempotency key makes a retried write safe.
		return 0, true
	}
	return 0, false
}

func (t *retryTransport) backoff(attempt int) time.Duration {
	d := t.policy.Base
	if d <= 0 {
		d = time.Second
	}
	for i := 1; i < attempt; i++ {
		d *= 2
		if t.policy.Max > 0 && d >= t.policy.Max {
			d = t.policy.Max
			break
		}
	}
	// Full jitter. Without it a fleet of clients that hit the same limit
	// retries in lockstep and rebuilds the spike it is backing off from.
	return time.Duration(rand.Int64N(int64(d)) + int64(d)/2)
}

// retryAfter reads the header in both forms RFC 9110 allows: delay-seconds and
// an HTTP-date. This API sends seconds; the date form is here because a proxy
// in front of it may not.
func retryAfter(h http.Header, now time.Time) (time.Duration, bool) {
	v := h.Get("Retry-After")
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	if at, err := http.ParseTime(v); err == nil {
		if d := at.Sub(now); d > 0 {
			return d, true
		}
		return 0, true
	}
	return 0, false
}

// hasCode peeks at a problem document without consuming it for the caller. The
// body is small (a problem document), and the peeked bytes are put back so the
// generated response parser still sees a complete stream.
func hasCode(resp *http.Response, code string) bool {
	if resp.Body == nil {
		return false
	}
	buf, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	_ = resp.Body.Close()
	// Put the bytes back either way: the generated response parser reads this
	// same body, and a peek that consumed it would turn every retryable 409
	// into an unparseable one.
	resp.Body = io.NopCloser(bytes.NewReader(buf))
	if err != nil {
		return false
	}
	return strings.Contains(string(buf), code)
}
