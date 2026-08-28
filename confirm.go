package firstboot

import (
	"context"
	"net/http"

	"github.com/firstboot-io/go-sdk/fbapi"
)

// ConfirmHeader is how a caller names what it is about to destroy.
//
// The platform requires it on `destroy`-scoped operations when the credential is
// one an APPLICATION was granted through the authorization flow, rather than one
// a customer minted by hand. The distinction is deliberate: a token in a script
// was typed by a person, while a granted credential may be held by a model, and
// a model's own claim that it meant to delete something is not a confirmation
// anybody outside that process has checked.
//
// It carries the id of the resource rather than a boolean on purpose. A flag
// would be set once by a wrapper and stay true for every call afterwards, which
// is a configuration; naming the resource means a header left over from the
// previous call refuses the next one.
const ConfirmHeader = "X-Firstboot-Confirm"

// Confirm names the resource a destructive request is about to act on.
//
// Pass it to the generated method as a request editor:
//
//	c.API.ServerDeleteWithResponse(ctx, id, &fbapi.ServerDeleteParams{}, firstboot.Confirm(id))
//
// The value must be what the URL names -- the last path parameter -- because
// that is what the platform compares it against. Confirming the server while
// deleting one of its snapshots is refused, which is the point.
func Confirm(target string) fbapi.RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		req.Header.Set(ConfirmHeader, target)
		return nil
	}
}
