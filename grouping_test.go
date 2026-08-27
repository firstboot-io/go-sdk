package firstboot

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/firstboot-io/firstboot-go/fbapi"
)

// What this pins is the WIRE shape of a tag filter.
//
// The API's filter is a containment test and the parameter is `explode`, so two
// tags travel as `?tag=a&tag=b`. A client that joined them into `?tag=a,b`
// would send one tag nothing carries and get an empty list back — a silent
// wrong answer, not an error, and exactly the answer a Terraform data source
// would then use to build an empty backend list.
func TestTagFilterTravelsAsRepeatedParameters(t *testing.T) {
	var got url.Values
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"servers":[],"total":0,"summary":{"total":0,"running":0,"monthly_charge_minor":0,"charge_currency":"TRY"}}`))
	}))

	for range c.Servers(context.Background(), ServersWithTags("env:prod", "role:web")) {
		break
	}

	tags := got["tag"]
	if len(tags) != 2 || tags[0] != "env:prod" || tags[1] != "role:web" {
		t.Fatalf("want two separate tag parameters, got %q (raw query %q)", tags, got.Encode())
	}
	if strings.Contains(got.Encode(), "env%3Aprod%2C") {
		t.Fatal("tags were joined with a comma; the API would read that as one tag")
	}
}

// A walk with no tag option must send no tag parameter at all. An empty
// `?tag=` is a tag whose name is the empty string, which matches nothing.
func TestNoTagOptionSendsNoTagParameter(t *testing.T) {
	var got url.Values
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"volumes":[],"total":0}`))
	}))

	for range c.Volumes(context.Background()) {
		break
	}

	if _, present := got["tag"]; present {
		t.Fatalf("an unfiltered walk sent a tag parameter: %q", got.Encode())
	}
}

// The project filter takes the literal "none", which is a question a UUID
// cannot ask: "in no project at all" is not the same as "in any project".
func TestProjectNoneReachesTheWire(t *testing.T) {
	var got url.Values
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"networks":[],"total":0}`))
	}))

	for range c.Networks(context.Background(), NetworksInProject("none")) {
		break
	}

	if got.Get("project") != "none" {
		t.Fatalf("want project=none, got %q", got.Encode())
	}
}

// Every groupable kind has a walk and both options. The eight are one decision
// on the server (internal/domain/grouping's table); a client that covers seven
// of them makes the eighth look unsupported.
func TestEveryGroupableKindCanBeWalkedAndFiltered(t *testing.T) {
	// Compile-time coverage: each line fails to build if the option is missing.
	var (
		_ ServerListOption       = ServersWithTags("a")
		_ ServerListOption       = ServersInProject("none")
		_ VolumeListOption       = VolumesWithTags("a")
		_ VolumeListOption       = VolumesInProject("none")
		_ NetworkListOption      = NetworksWithTags("a")
		_ NetworkListOption      = NetworksInProject("none")
		_ DatabaseListOption     = DatabasesWithTags("a")
		_ DatabaseListOption     = DatabasesInProject("none")
		_ LoadBalancerListOption = LoadBalancersWithTags("a")
		_ LoadBalancerListOption = LoadBalancersInProject("none")
		_ ZoneListOption         = ZonesWithTags("a")
		_ ZoneListOption         = ZonesInProject("none")
		_ AppListOption          = AppsWithTags("a")
		_ AppListOption          = AppsInProject("none")
		_ DomainListOption       = DomainsWithTags("a")
		_ DomainListOption       = DomainsInProject("none")
	)

	// And each params struct really does carry both fields, which is what the
	// options write into.
	var (
		_ *[]string = (&fbapi.ServersListParams{}).Tag
		_ *[]string = (&fbapi.VolumeListParams{}).Tag
		_ *[]string = (&fbapi.NetworksListParams{}).Tag
		_ *[]string = (&fbapi.DatabasesListParams{}).Tag
		_ *[]string = (&fbapi.LoadBalancersListParams{}).Tag
		_ *[]string = (&fbapi.DnsZonesListParams{}).Tag
		_ *[]string = (&fbapi.AppsListParams{}).Tag
		_ *[]string = (&fbapi.DomainsListParams{}).Tag
	)
}
