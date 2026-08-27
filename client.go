// Package firstboot is the Go client for the Firstboot API.
//
// It is two layers. `fbapi` is generated from the platform's own
// `openapi.json` and is never edited by hand: every endpoint, every request and
// response type, every state enum. This package is the layer the generator
// cannot write, and it exists because the hard part of talking to this API is
// not sending a request:
//
//   - Knowing when a create is FINISHED. A create answers 202 and the resource
//     converges in the background, so the client has to poll until the state is
//     one that has settled. Which values those are differs per resource, and a
//     value the client does not recognise must count as still working.
//   - Retrying WITHOUT creating a second server. Every create accepts an
//     `Idempotency-Key`; this client sets one automatically and reuses it across
//     its own retries, which is the whole reason a retry here is safe.
//   - Reading a rate limit's answer instead of guessing at it. A refused create
//     carries `Retry-After`, measured from the moment a slot actually frees.
//
// The three consumers this was built for (a Terraform provider, a CLI and an
// MCP server) all need those three things and would otherwise each write their
// own, differently.
package firstboot

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/firstboot-io/go-sdk/fbapi"
)

// Environment variables the client reads when an option is not given. They are
// the names the customer documentation already uses, so a reader who followed
// the API guide has them exported already.
const (
	EnvBaseURL = "FIRSTBOOT_API_URL"
	EnvToken   = "FIRSTBOOT_TOKEN"
)

// DefaultUserAgent identifies this client in the platform's access log. Worth
// setting: "which client is hammering this endpoint" is otherwise answerable
// only by IP, and a CI runner's IP says nothing.
const DefaultUserAgent = "firstboot-go"

// Client talks to one Firstboot account.
//
// An API token is pinned to one organization for the life of the token, so a
// Client IS an organization: there is deliberately no per-call organization
// parameter and no switcher. Two organizations means two Clients.
type Client struct {
	// API is the generated client. Exported on purpose: this package wraps the
	// calls that need wrapping and gets out of the way for the ~190 that do
	// not. A consumer reaching straight for c.API is using this library
	// correctly, not working around it.
	API *fbapi.ClientWithResponses

	baseURL string
	token   string
	ua      string
	http    *http.Client
	retry   RetryPolicy
	// autoIdempotency is on by default. See idempotency.go for what it does and
	// what it deliberately does not do.
	autoIdempotency bool
	now             func() time.Time
}

// Option configures a Client. Options are applied in order, so a later one
// wins, which is what lets a caller override an environment default.
type Option func(*Client)

// WithBaseURL sets the API origin, e.g. https://api.example.com. Overrides
// FIRSTBOOT_API_URL.
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = u } }

// WithToken sets the API token (the `pat_` credential minted in the panel).
// Overrides FIRSTBOOT_TOKEN.
func WithToken(t string) Option { return func(c *Client) { c.token = t } }

// WithHTTPClient replaces the underlying client. The retry and idempotency
// transports are layered ON TOP of whatever Transport it carries, so a caller
// supplying an instrumented client keeps their instrumentation.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// WithRetry replaces the retry policy. A zero RetryPolicy disables retrying.
func WithRetry(p RetryPolicy) Option { return func(c *Client) { c.retry = p } }

// WithUserAgent appends a product token to the User-Agent, e.g.
// "terraform-provider-firstboot/0.1.0". The library's own token stays.
func WithUserAgent(ua string) Option { return func(c *Client) { c.ua = ua } }

// WithoutAutoIdempotency stops the client generating an `Idempotency-Key` for
// creates that do not carry one.
//
// There is one honest reason to use it and it is not performance: a caller that
// manages keys itself across process restarts, where a key generated per
// process is the wrong scope. Turning it off to "keep requests simple" removes
// the protection that makes this client's retries safe.
func WithoutAutoIdempotency() Option { return func(c *Client) { c.autoIdempotency = false } }

// New builds a Client. It fails rather than defaulting when the base URL or the
// token is missing: a client pointed at nothing produces a connection error
// pages later, and a client with no token produces 401s that read like a
// permissions problem.
func New(opts ...Option) (*Client, error) {
	c := &Client{
		baseURL:         strings.TrimSpace(os.Getenv(EnvBaseURL)),
		token:           strings.TrimSpace(os.Getenv(EnvToken)),
		ua:              "",
		retry:           DefaultRetryPolicy(),
		autoIdempotency: true,
		now:             time.Now,
	}
	for _, o := range opts {
		o(c)
	}
	if c.baseURL == "" {
		return nil, fmt.Errorf("firstboot: no API base URL: set %s or use WithBaseURL", EnvBaseURL)
	}
	if c.token == "" {
		return nil, fmt.Errorf("firstboot: no API token: set %s or use WithToken", EnvToken)
	}
	// A token that is not a token is worth catching here rather than as a 401
	// three calls later. The prefix is public and fixed; the entropy is what
	// follows it, and this checks neither.
	if !strings.HasPrefix(c.token, "pat_") {
		return nil, errors.New(`firstboot: the token does not look like one (API tokens begin with "pat_")`)
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: 60 * time.Second}
	}

	// Transport order, outermost first:
	//
	//	idempotency -> retry -> the caller's transport
	//
	// Idempotency has to be OUTSIDE retry, or each attempt would mint its own
	// key and the retry would create a second resource -- the exact failure the
	// header exists to prevent. Retry has to be outside the caller's transport
	// so an instrumented client sees one span per attempt rather than one per
	// logical call, which is what makes a retried request visible at all.
	base := c.http.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	wrapped := *c.http
	wrapped.Transport = &idempotencyTransport{
		next:    &retryTransport{next: base, policy: c.retry, now: c.now},
		enabled: c.autoIdempotency,
	}

	api, err := fbapi.NewClientWithResponses(
		strings.TrimRight(c.baseURL, "/"),
		fbapi.WithHTTPClient(&wrapped),
		fbapi.WithRequestEditorFn(c.authorize),
	)
	if err != nil {
		return nil, fmt.Errorf("firstboot: building the client: %w", err)
	}
	c.API = api
	return c, nil
}

// authorize is a RequestEditorFn rather than a transport because it is about
// the REQUEST rather than about how it is sent, and because putting it here
// keeps the credential out of any transport a caller might swap in.
func (c *Client) authorize(_ context.Context, req *http.Request) error {
	req.Header.Set("Authorization", "Bearer "+c.token)
	ua := DefaultUserAgent
	if c.ua != "" {
		ua = c.ua + " " + ua
	}
	req.Header.Set("User-Agent", ua)
	return nil
}
