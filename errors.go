package firstboot

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/firstboot-io/firstboot-go/fbapi"
)

// The API's refusals, as Go errors.
//
// Every 4xx this API returns carries a machine-readable code in the problem
// document's `detail` -- `NO_CAPACITY_IN_REGION`, `PLAN_NOT_OFFERED_IN_REGION`,
// `IDEMPOTENCY_KEY_REUSED`. Those codes are the contract; the sentence around
// them is not. Turning them into typed errors here means the three consumers of
// this library branch on the same values rather than each doing their own
// string comparison against a field whose wording can change.

// APIError is any refusal the API expressed as a problem document.
type APIError struct {
	// Status is the HTTP status. Kept because the code alone does not say
	// whether waiting could help.
	Status int
	// Code is the machine-readable half: the leading token of `detail`, which
	// this API writes as SCREAMING_SNAKE. Empty when the response carried none,
	// which is the shape of a 500 -- those deliberately drop their detail.
	Code string
	// Detail is the whole `detail` field, code included. Some codes carry a
	// human sentence after them (USER_DATA_NOT_SUPPORTED does); it is here
	// rather than parsed off, because parsing it would invent a second contract.
	Detail string
	// Title is the problem document's title, e.g. "Unprocessable Entity".
	Title string
	// RequestID is the platform's own request id when the response carried one.
	// It is the join key between what a caller saw and what the operator's log
	// says, and quoting it in a support ticket is the difference between a
	// diagnosis and a guess.
	RequestID string
}

func (e *APIError) Error() string {
	// The message the caller sees, in the order of what actually helps them.
	// Status is appended only when there IS one: the sentinels below carry a
	// code and nothing else, and printing "HTTP 0" beside a real error code
	// reads as a client bug rather than as the API's answer.
	msg := e.Detail
	if msg == "" {
		msg = e.Code
	}
	if msg == "" {
		msg = e.Title
	}
	switch {
	case msg != "" && e.Status != 0:
		return fmt.Sprintf("firstboot: %s (HTTP %d)", msg, e.Status)
	case msg != "":
		return "firstboot: " + msg
	default:
		return fmt.Sprintf("firstboot: HTTP %d", e.Status)
	}
}

// Is lets callers compare against the sentinels below without unwrapping.
func (e *APIError) Is(target error) bool {
	var t *APIError
	if !errors.As(target, &t) {
		return false
	}
	// A sentinel carries only a code; a real error carries everything. Matching
	// on the code alone is what makes errors.Is(err, ErrNoCapacity) work.
	return t.Code != "" && t.Code == e.Code
}

// Sentinels for the refusals a caller can actually do something about. This is
// deliberately not every code the API can return: a list that tried to be
// exhaustive would go stale silently, while a caller comparing
// `apiErr.Code == "SOMETHING_NEW"` always works.
var (
	// ErrNoCapacity: the region has no host with room. Retrying later can work;
	// retrying immediately cannot.
	ErrNoCapacity = &APIError{Code: "NO_CAPACITY_IN_REGION"}
	// ErrPlanNotOffered: no host in that region sells the plan. Waiting does
	// not help -- this is a catalog fact, not a capacity one, and the two used
	// to be one 503 that told customers to wait for room that would not have
	// helped.
	ErrPlanNotOffered = &APIError{Code: "PLAN_NOT_OFFERED_IN_REGION"}
	// ErrInsufficientBalance: the wallet cannot cover the first month.
	ErrInsufficientBalance = &APIError{Code: "INSUFFICIENT_BALANCE"}
	// ErrCreateCooldown: too many resources created in the rolling window. The
	// response carries Retry-After and this client already waited on it, so
	// seeing this error means the wait exceeded the retry policy's budget.
	ErrCreateCooldown = &APIError{Code: "CREATE_COOLDOWN"}
	// ErrIdempotencyKeyReused: the same key was sent with a different body.
	// Never retryable as sent -- the caller is generating one key for two
	// requests, which is almost always a key built outside its loop.
	ErrIdempotencyKeyReused = &APIError{Code: "IDEMPOTENCY_KEY_REUSED"}
	// ErrIdempotencyConflict: another request holding the same key committed
	// between the lookup and the insert. Retryable, and this client retries it.
	ErrIdempotencyConflict = &APIError{Code: "IDEMPOTENCY_CONFLICT"}
	// ErrQuotaExceeded covers both levels; the API distinguishes them and a
	// caller almost never can act on the difference.
	ErrQuotaExceeded = &APIError{Code: "QUOTA_EXCEEDED"}
	// ErrOrganizationSuspended: nothing new is provisioned for this account.
	ErrOrganizationSuspended = &APIError{Code: "ORGANIZATION_SUSPENDED"}
)

// StateError is returned by the waiters when a resource settles into a failure
// rather than into success. It is NOT an APIError: every request involved
// succeeded, and the thing that failed is the work.
type StateError struct {
	Kind  string // "server", "volume", ...
	ID    string
	State string
	// Code is the resource's own error_code when it carries one. A server has
	// it; not every resource does.
	Code string
}

func (e *StateError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("firstboot: %s %s failed: %s (%s)", e.Kind, e.ID, e.State, e.Code)
	}
	return fmt.Sprintf("firstboot: %s %s failed: %s", e.Kind, e.ID, e.State)
}

// TimeoutError is returned when a waiter's budget ran out. It reports the LAST
// state seen rather than only "timed out", because those are two different
// conversations: a server still in `provisioning` after ten minutes is a
// platform question, one that reached `stopped` is the caller's own.
type TimeoutError struct {
	Kind      string
	ID        string
	LastState string
	Waited    string
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("firstboot: gave up waiting for %s %s after %s; last state was %q",
		e.Kind, e.ID, e.Waited, e.LastState)
}

// ErrorFrom builds an APIError from a generated response's parsed problem
// document. Exported because every consumer of this library needs it: the
// generated methods hand back a typed response rather than an error, so turning
// a non-2xx into something errors.Is can match is the caller's job, and three
// callers doing it three ways is three subtly different error surfaces.
//
//	if resp.JSON200 == nil {
//	        return firstboot.ErrorFrom(resp.StatusCode(),
//	                resp.ApplicationproblemJSONDefault, resp.HTTPResponse.Header)
//	}
func ErrorFrom(status int, model *fbapi.ErrorModel, header http.Header) error {
	return errorFrom(status, model, header)
}

// CodeFromDetail pulls the machine-readable code off a problem document's
// `detail`. Exported for the same reason as ErrorFrom: a caller holding a
// detail string from somewhere else should not re-derive the rule.
func CodeFromDetail(detail string) string { return leadingCode(detail) }

// errorFrom builds an APIError from a parsed problem document. The generated
// responses expose the document as *fbapi.ErrorModel on a
// `ApplicationproblemJSONDefault` field; a response that fell outside every
// declared status has none, so the status and the request id are all there is.
func errorFrom(status int, model *fbapi.ErrorModel, header http.Header) *APIError {
	e := &APIError{Status: status}
	if header != nil {
		e.RequestID = header.Get("X-Request-Id")
	}
	if model == nil {
		return e
	}
	if model.Title != nil {
		e.Title = *model.Title
	}
	if model.Detail != nil {
		e.Detail = *model.Detail
		e.Code = leadingCode(*model.Detail)
	}
	return e
}

// leadingCode pulls the SCREAMING_SNAKE token off the front of a detail string.
// A detail with no such token (huma's own validation messages, for one) yields
// an empty code rather than a wrong one.
func leadingCode(detail string) string {
	for i := 0; i < len(detail); i++ {
		ch := detail[i]
		switch {
		case ch >= 'A' && ch <= 'Z', ch == '_', ch >= '0' && ch <= '9':
			continue
		case ch == ':' || ch == ' ':
			if i == 0 {
				return ""
			}
			return detail[:i]
		default:
			return ""
		}
	}
	return detail
}
