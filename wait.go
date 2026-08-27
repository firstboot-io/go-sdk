package firstboot

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/firstboot-io/go-sdk/fbapi"
)

// Waiting for a resource to converge.
//
// This is the piece a generated client cannot produce and the reason every
// consumer would otherwise write it again, differently: Terraform needs it to
// finish a Create, the CLI needs it behind `--wait`, and an MCP server needs it
// to answer "is the server ready" in one turn instead of teaching a model to
// poll. One implementation, one backoff, one definition of finished.

// WaitOptions bounds a wait.
type WaitOptions struct {
	// Timeout is the total budget. Zero means DefaultWaitTimeout.
	Timeout time.Duration
	// Interval is the first poll delay; it doubles up to MaxInterval. Zero
	// means the schedule the customer documentation prescribes (1s to 15s).
	Interval    time.Duration
	MaxInterval time.Duration
	// OnState, when set, is called with every state observed, including
	// repeats. It exists so a CLI can render progress and a Terraform provider
	// can log one line per transition without either of them re-polling.
	OnState func(state string)
}

// DefaultWaitTimeout is generous on purpose. A create is measured against
// "SSH open in 77 s" on real hardware, but a cold template on a busy host is
// slower and a caller that gave up at two minutes would report a failure the
// platform did not have.
const DefaultWaitTimeout = 15 * time.Minute

// The three waits whose work is not this platform's, and whose budgets
// therefore have nothing to do with how fast a server boots.
const (
	// DefaultISOWaitTimeout covers a multi-gigabyte download from a URL the
	// customer chose, over a link nobody here controls.
	DefaultISOWaitTimeout = 60 * time.Minute
	// DefaultBuildWaitTimeout covers an image build: dependency install,
	// compile, push. A cold cache on a first deploy is the slow case.
	DefaultBuildWaitTimeout = 30 * time.Minute
	// DefaultDomainWaitTimeout covers a REGISTRY's answer. Long enough for the
	// slow TLDs and nowhere near long enough for a transfer, which takes days --
	// a caller waiting on one has to say so with WithTimeout.
	DefaultDomainWaitTimeout = 20 * time.Minute
)

// WaitOption is the functional form, for callers who want one knob.
type WaitOption func(*WaitOptions)

// WithTimeout bounds the total wait.
func WithTimeout(d time.Duration) WaitOption { return func(o *WaitOptions) { o.Timeout = d } }

// WithProgress reports every observed state.
func WithProgress(f func(state string)) WaitOption { return func(o *WaitOptions) { o.OnState = f } }

func (o WaitOptions) normalize() WaitOptions {
	if o.Timeout <= 0 {
		o.Timeout = DefaultWaitTimeout
	}
	if o.Interval <= 0 {
		o.Interval = time.Second
	}
	if o.MaxInterval <= 0 {
		o.MaxInterval = 15 * time.Second
	}
	return o
}

// poll runs the shared loop. read returns the current state and whatever the
// caller wants back; a read that errors does NOT end the wait, because a single
// failed poll is a network blip and ending on it would report a provisioning
// failure that never happened. Only the budget ends the wait.
func poll[T any](
	ctx context.Context,
	kind, id string,
	opts WaitOptions,
	read func(context.Context) (state string, out T, err error),
) (T, error) {
	opts = opts.normalize()
	var zero T

	deadline := time.Now().Add(opts.Timeout)
	interval := opts.Interval
	last := ""

	for {
		state, out, err := read(ctx)
		if err != nil {
			// Remember nothing and try again; the budget is the only exit.
			if ctx.Err() != nil {
				return zero, ctx.Err()
			}
		} else {
			last = state
			if opts.OnState != nil {
				opts.OnState(state)
			}
			switch Classify(kind, state) {
			case Ready, Settled:
				return out, nil
			case Failed:
				return out, &StateError{Kind: kind, ID: id, State: state, Code: errorCodeOf(out)}
			}
		}

		if time.Now().After(deadline) {
			return zero, &TimeoutError{
				Kind: kind, ID: id, LastState: last, Waited: opts.Timeout.String(),
			}
		}
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(interval):
		}
		if interval *= 2; interval > opts.MaxInterval {
			interval = opts.MaxInterval
		}
	}
}

// errorCodeOf pulls the reason a resource publishes for its own failure.
//
// Which field that is differs, and the difference is the API's rather than this
// package's: a server, a load balancer and a database carry a machine-readable
// `error_code`, a domain carries `last_error_code`, and an ISO and a build carry
// only a sentence. Both kinds land in StateError.Code because a caller staring
// at a failed resource wants the reason, and having two fields where one is
// always empty helps nobody.
func errorCodeOf(v any) string {
	switch t := v.(type) {
	case *fbapi.ServerBody:
		if t != nil && t.ErrorCode != nil {
			return *t.ErrorCode
		}
	case *fbapi.LoadBalancerBody:
		if t != nil && t.ErrorCode != nil {
			return *t.ErrorCode
		}
	case *fbapi.DatabaseBody:
		if t != nil && t.ErrorCode != nil {
			return *t.ErrorCode
		}
	case *fbapi.DomainBody:
		if t != nil && t.LastErrorCode != nil {
			return *t.LastErrorCode
		}
	case *fbapi.IsoBody:
		if t != nil && t.ErrorMessage != nil {
			return *t.ErrorMessage
		}
	case *fbapi.BuildBody:
		if t != nil && t.Error != nil {
			return *t.Error
		}
	}
	return ""
}

// WaitForServer polls until the server has settled, and returns it.
//
// A server that settles into `stopped` is returned WITHOUT an error: it is a
// real, finished, billable machine, and a library that called that a failure
// would be making a product judgement it has no standing to make. Only an
// `error_*` state produces a *StateError.
func (c *Client) WaitForServer(ctx context.Context, id string, opts ...WaitOption) (*fbapi.ServerBody, error) {
	o := applyWaitOptions(opts)
	return poll(ctx, "server", id, o, func(ctx context.Context) (string, *fbapi.ServerBody, error) {
		resp, err := c.API.ServerGetWithResponse(ctx, id)
		if err != nil {
			return "", nil, err
		}
		if resp.JSON200 == nil {
			return "", nil, errorFrom(resp.StatusCode(), resp.ApplicationproblemJSONDefault, header(resp.HTTPResponse))
		}
		return string(resp.JSON200.State), resp.JSON200, nil
	})
}

// WaitForVolume polls until the volume has settled.
func (c *Client) WaitForVolume(ctx context.Context, id uuid.UUID, opts ...WaitOption) (*fbapi.VolumeBody, error) {
	o := applyWaitOptions(opts)
	return poll(ctx, "volume", id.String(), o, func(ctx context.Context) (string, *fbapi.VolumeBody, error) {
		resp, err := c.API.VolumeGetWithResponse(ctx, id.String())
		if err != nil {
			return "", nil, err
		}
		if resp.JSON200 == nil {
			return "", nil, errorFrom(resp.StatusCode(), resp.ApplicationproblemJSONDefault, header(resp.HTTPResponse))
		}
		return string(resp.JSON200.State), resp.JSON200, nil
	})
}

// WaitForServerAction polls one power action to its own terminal state.
//
// Separate from WaitForServer because the SERVER's state is not the answer for
// every action: a reboot leaves it `running` throughout, so a caller watching
// the server sees nothing happen and cannot tell success from a request that
// was never applied.
func (c *Client) WaitForServerAction(ctx context.Context, serverID string, actionID uuid.UUID, opts ...WaitOption) (*fbapi.ActionBody, error) {
	o := applyWaitOptions(opts)
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Minute
	}
	return poll(ctx, "action", actionID.String(), o, func(ctx context.Context) (string, *fbapi.ActionBody, error) {
		resp, err := c.API.ServerActionGetWithResponse(ctx, serverID, actionID.String())
		if err != nil {
			return "", nil, err
		}
		if resp.JSON200 == nil {
			return "", nil, errorFrom(resp.StatusCode(), resp.ApplicationproblemJSONDefault, header(resp.HTTPResponse))
		}
		return string(resp.JSON200.State), resp.JSON200, nil
	})
}

// WaitForLoadBalancer polls until the load balancer has settled.
//
// A load balancer answers 202 and its data plane is configured afterwards, so
// the address in the create's response answers nothing until this returns.
func (c *Client) WaitForLoadBalancer(ctx context.Context, id string, opts ...WaitOption) (*fbapi.LoadBalancerBody, error) {
	o := applyWaitOptions(opts)
	return poll(ctx, "load_balancer", id, o, func(ctx context.Context) (string, *fbapi.LoadBalancerBody, error) {
		resp, err := c.API.LoadBalancerGetWithResponse(ctx, id)
		if err != nil {
			return "", nil, err
		}
		if resp.JSON200 == nil {
			return "", nil, errorFrom(resp.StatusCode(), resp.ApplicationproblemJSONDefault, header(resp.HTTPResponse))
		}
		lb := &resp.JSON200.LoadBalancer
		return string(lb.State), lb, nil
	})
}

// WaitForDatabase polls until the database instance has settled.
//
// It does NOT wait for `pending_apply` to clear. That flag tracks an edit
// reaching the appliance, which is a second thing to wait for and a different
// one: a resize leaves the state `active` throughout and only the flag moves.
// Waiting on the state here keeps this waiter answering one question.
func (c *Client) WaitForDatabase(ctx context.Context, id string, opts ...WaitOption) (*fbapi.DatabaseBody, error) {
	o := applyWaitOptions(opts)
	return poll(ctx, "database", id, o, func(ctx context.Context) (string, *fbapi.DatabaseBody, error) {
		resp, err := c.API.DatabaseGetWithResponse(ctx, id)
		if err != nil {
			return "", nil, err
		}
		if resp.JSON200 == nil {
			return "", nil, errorFrom(resp.StatusCode(), resp.ApplicationproblemJSONDefault, header(resp.HTTPResponse))
		}
		db := &resp.JSON200.Database
		return string(db.State), db, nil
	})
}

// WaitForISO polls until a custom ISO has downloaded.
//
// The budget is its own: an ISO is a multi-gigabyte fetch from a URL the
// customer chose, over a link this platform does not control, so the default
// that suits a server create is far too short for it.
func (c *Client) WaitForISO(ctx context.Context, id string, opts ...WaitOption) (*fbapi.IsoBody, error) {
	o := applyWaitOptions(opts)
	if o.Timeout == 0 {
		o.Timeout = DefaultISOWaitTimeout
	}
	return poll(ctx, "iso", id, o, func(ctx context.Context) (string, *fbapi.IsoBody, error) {
		resp, err := c.API.IsoGetWithResponse(ctx, id)
		if err != nil {
			return "", nil, err
		}
		if resp.JSON200 == nil {
			return "", nil, errorFrom(resp.StatusCode(), resp.ApplicationproblemJSONDefault, header(resp.HTTPResponse))
		}
		iso := &resp.JSON200.Iso
		return string(iso.Status), iso, nil
	})
}

// WaitForBuild polls one app build to its own terminal state.
//
// Separate from the app for the same reason a power action is separate from its
// server: an app that is already running keeps running while a new version
// builds, so watching the APP shows nothing happening and cannot tell a
// finished build from one that never started. The build is the thing with an
// answer.
//
// A cancelled build returns WITHOUT an error. It is terminal and it is the
// customer's own doing.
func (c *Client) WaitForBuild(ctx context.Context, appCode, buildID string, opts ...WaitOption) (*fbapi.BuildBody, error) {
	o := applyWaitOptions(opts)
	if o.Timeout == 0 {
		o.Timeout = DefaultBuildWaitTimeout
	}
	return poll(ctx, "build", buildID, o, func(ctx context.Context) (string, *fbapi.BuildBody, error) {
		resp, err := c.API.AppBuildGetWithResponse(ctx, appCode, buildID)
		if err != nil {
			return "", nil, err
		}
		if resp.JSON200 == nil {
			return "", nil, errorFrom(resp.StatusCode(), resp.ApplicationproblemJSONDefault, header(resp.HTTPResponse))
		}
		return string(resp.JSON200.State), resp.JSON200, nil
	})
}

// WaitForDomain polls a registration until the registry has answered.
//
// The budget is its own and it is long, because the thing being waited on is
// not this platform: a registry answers a register in seconds for most TLDs and
// in minutes for some, and a transfer is measured in days rather than minutes.
// A caller waiting on a transfer should say so with WithTimeout rather than
// trust any default.
//
// A domain that settles into `expired` or `redemption` is returned WITHOUT an
// error: the name was really registered and really lapsed later, which is not a
// failure of the call that bought it.
func (c *Client) WaitForDomain(ctx context.Context, id string, opts ...WaitOption) (*fbapi.DomainBody, error) {
	o := applyWaitOptions(opts)
	if o.Timeout == 0 {
		o.Timeout = DefaultDomainWaitTimeout
	}
	return poll(ctx, "domain", id, o, func(ctx context.Context) (string, *fbapi.DomainBody, error) {
		resp, err := c.API.DomainGetWithResponse(ctx, id)
		if err != nil {
			return "", nil, err
		}
		if resp.JSON200 == nil {
			return "", nil, errorFrom(resp.StatusCode(), resp.ApplicationproblemJSONDefault, header(resp.HTTPResponse))
		}
		d := &resp.JSON200.Domain
		return string(d.State), d, nil
	})
}

func applyWaitOptions(opts []WaitOption) WaitOptions {
	var o WaitOptions
	for _, f := range opts {
		f(&o)
	}
	return o
}

func header(r *http.Response) http.Header {
	if r == nil {
		return nil
	}
	return r.Header
}
