package firstboot

import (
	"context"
	"net/http"
	"net/url"
	"testing"
)

// The region and a state bucket reach the wire as the API names them, together
// with any other filter, so a narrowed walk is narrowed on the server rather
// than over one page in the client.
func TestServerRegionAndStateBucketReachTheWire(t *testing.T) {
	var got url.Values
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"servers":[],"total":0}`))
	}))

	for range c.Servers(context.Background(), ServersInRegion("ist"), ServersInState("error")) {
		break
	}

	if got.Get("region") != "ist" {
		t.Fatalf("region = %q, want ist (raw query %q)", got.Get("region"), got.Encode())
	}
	if got.Get("state") != "error" {
		t.Fatalf("state = %q, want error (raw query %q)", got.Get("state"), got.Encode())
	}
}
