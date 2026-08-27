package firstboot

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/firstboot-io/go-sdk/fbapi"
)

// The header that makes a retry safe, set for the caller.
//
// Every create in this API accepts `Idempotency-Key`, and a create sent without
// one is a create the caller has said it is willing to repeat. That default is
// right for the API and wrong for a client library: the moment this package
// retries a request whose response was lost, an absent key turns one create into
// two servers and two months of billing.
//
// So the client mints one. The rule that makes it correct is that it mints the
// key ONCE PER LOGICAL REQUEST rather than per attempt, which is why this
// transport wraps the retry transport rather than sitting under it: the header
// is written before the first send, and every retry below re-sends the same
// request object with the same header.
//
// # What this deliberately does NOT do
//
// It does not survive the process. A key generated here is a fresh UUID with no
// memory, so a caller that crashes after sending a create and retries on the
// next run gets a new key and a second resource. Nothing a library can do fixes
// that -- the key has to be derived from something the CALLER considers stable
// (a Terraform resource address, a job id) and stored where the caller stores
// its state. A caller doing that sets the header itself, and this transport
// leaves it alone.
type idempotencyTransport struct {
	next    http.RoundTripper
	enabled bool
}

// IdempotencyHeader is the header name, exported because a caller managing its
// own keys should not have to spell it.
const IdempotencyHeader = "Idempotency-Key"

func (t *idempotencyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.enabled && req.Method == http.MethodPost && req.Header.Get(IdempotencyHeader) == "" {
		// POST only. It is the method every create uses, and the API ignores
		// the header everywhere else, so a stray one on a POST that is really
		// an action (a reboot, a build) costs nothing and needs no table here
		// of which paths are creates -- a table this package would have to keep
		// in step with an API it does not own.
		req.Header.Set(IdempotencyHeader, uuid.NewString())
	}
	return t.next.RoundTrip(req)
}

// WithIdempotencyKey returns a request editor that pins one key to one call.
// Use it when the key has to mean something outside this process:
//
//	resp, err := c.API.ServerCreateWithResponse(ctx, params, body,
//	        firstboot.WithIdempotencyKey("tf:"+resourceAddress))
//
// A key must identify the REQUEST, not the caller: the same key with a
// different body is refused with IDEMPOTENCY_KEY_REUSED, which is the API
// telling you the key was built outside the loop that varies.
func WithIdempotencyKey(key string) fbapi.RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		req.Header.Set(IdempotencyHeader, key)
		return nil
	}
}
