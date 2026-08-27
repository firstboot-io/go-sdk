package firstboot

import (
	"context"
	"iter"

	"github.com/firstboot-io/go-sdk/fbapi"
)

// Walking a paged list.
//
// Every growing list in this API takes `limit`/`offset` and answers with
// `total`. That is a fine contract and a tedious one: the three consumers of
// this library all want "give me every server" and would each write the same
// loop, each with its own off-by-one at the last page.
//
// The iterators below yield one item at a time and fetch a page when they run
// out. A caller that genuinely wants one page still calls the generated method
// directly -- these are for the "all of them" case, which is the common one in
// automation and the rare one in a panel.

// PageSize is the per-request page. 200 is the API's ceiling (a larger limit is
// REFUSED rather than clamped, so asking for more is an error, not a hint), and
// asking for the maximum is right here: an iterator's caller has already said
// it wants everything.
const PageSize = 200

// listPage is the shape every paged read shares once the generated types are
// out of the way.
type listPage[T any] struct {
	items []T
	total int64
}

// iterate is the shared loop. It stops on the page that did not fill, rather
// than on `offset >= total`: `total` is computed per request, so a list that
// grows while being walked would otherwise loop past the end, and one that
// shrinks would stop early.
//
// It does NOT try to be consistent across pages. Nothing in this API offers a
// cursor or a snapshot, so a resource created mid-walk may be missed and one
// deleted mid-walk may appear twice. Saying so here is better than a helper
// that quietly implies otherwise.
func iterate[T any](ctx context.Context, fetch func(ctx context.Context, limit, offset int32) (listPage[T], error)) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		var offset int32
		for {
			page, err := fetch(ctx, PageSize, offset)
			if err != nil {
				var zero T
				yield(zero, err)
				return
			}
			for _, item := range page.items {
				if !yield(item, nil) {
					return
				}
			}
			if len(page.items) < PageSize {
				return
			}
			offset += int32(len(page.items))
		}
	}
}

// Servers walks every server in the organization.
//
//	for srv, err := range c.Servers(ctx) {
//	        if err != nil { return err }
//	        fmt.Println(srv.Name, srv.State)
//	}
//
// The filters `serversList` accepts (search, state, project) are applied BEFORE
// paging by the API, so a filtered walk scans the whole account rather than one
// page. Pass them through opts.
func (c *Client) Servers(ctx context.Context, opts ...ServerListOption) iter.Seq2[fbapi.ServerBody, error] {
	var p fbapi.ServersListParams
	for _, o := range opts {
		o(&p)
	}
	return iterate(ctx, func(ctx context.Context, limit, offset int32) (listPage[fbapi.ServerBody], error) {
		q := p
		q.Limit, q.Offset = &limit, &offset
		resp, err := c.API.ServersListWithResponse(ctx, &q)
		if err != nil {
			return listPage[fbapi.ServerBody]{}, err
		}
		if resp.JSON200 == nil {
			return listPage[fbapi.ServerBody]{}, errorFrom(resp.StatusCode(), resp.ApplicationproblemJSONDefault, header(resp.HTTPResponse))
		}
		var items []fbapi.ServerBody
		if resp.JSON200.Servers != nil {
			items = *resp.JSON200.Servers
		}
		return listPage[fbapi.ServerBody]{items: items, total: resp.JSON200.Total}, nil
	})
}

// ServerListOption narrows a server walk.
type ServerListOption func(*fbapi.ServersListParams)

// SearchServers matches a name, IP address or image name, partially.
func SearchServers(q string) ServerListOption {
	return func(p *fbapi.ServersListParams) { p.Search = &q }
}

// ServersInState narrows to `running`, `stopped` or `other`. It is a BUCKET,
// not a state value: `other` is everything that is neither of the first two,
// which is why it cannot take an `error_provisioning`.
func ServersInState(bucket fbapi.ServersListParamsState) ServerListOption {
	return func(p *fbapi.ServersListParams) { p.State = &bucket }
}

// ServersInProject narrows to one project, or to the servers in none of them
// with the literal "none".
func ServersInProject(id string) ServerListOption {
	return func(p *fbapi.ServersListParams) { p.Project = &id }
}

// Volumes walks every volume in the organization.
//
// Note what this costs: a volume is billed at its provisioned size from
// creation to deletion whether or not it is attached, so a walk that finds
// detached ones is finding money.
func (c *Client) Volumes(ctx context.Context, opts ...VolumeListOption) iter.Seq2[fbapi.VolumeBody, error] {
	var p fbapi.VolumeListParams
	for _, o := range opts {
		o(&p)
	}
	return iterate(ctx, func(ctx context.Context, limit, offset int32) (listPage[fbapi.VolumeBody], error) {
		q := p
		q.Limit, q.Offset = &limit, &offset
		resp, err := c.API.VolumeListWithResponse(ctx, &q)
		if err != nil {
			return listPage[fbapi.VolumeBody]{}, err
		}
		if resp.JSON200 == nil {
			return listPage[fbapi.VolumeBody]{}, errorFrom(resp.StatusCode(), resp.ApplicationproblemJSONDefault, header(resp.HTTPResponse))
		}
		var items []fbapi.VolumeBody
		if resp.JSON200.Volumes != nil {
			items = *resp.JSON200.Volumes
		}
		return listPage[fbapi.VolumeBody]{items: items, total: resp.JSON200.Total}, nil
	})
}

// VolumeListOption narrows a volume walk.
type VolumeListOption func(*fbapi.VolumeListParams)

// VolumesOnServer narrows to the volumes attached to one server.
func VolumesOnServer(id string) VolumeListOption {
	return func(p *fbapi.VolumeListParams) { p.ServerId = &id }
}

// Networks walks every private network in the organization.
func (c *Client) Networks(ctx context.Context, opts ...NetworkListOption) iter.Seq2[fbapi.NetworkBody, error] {
	var p fbapi.NetworksListParams
	for _, o := range opts {
		o(&p)
	}
	return iterate(ctx, func(ctx context.Context, limit, offset int32) (listPage[fbapi.NetworkBody], error) {
		q := p
		q.Limit, q.Offset = &limit, &offset
		resp, err := c.API.NetworksListWithResponse(ctx, &q)
		if err != nil {
			return listPage[fbapi.NetworkBody]{}, err
		}
		if resp.JSON200 == nil {
			return listPage[fbapi.NetworkBody]{}, errorFrom(resp.StatusCode(), resp.ApplicationproblemJSONDefault, header(resp.HTTPResponse))
		}
		var items []fbapi.NetworkBody
		if resp.JSON200.Networks != nil {
			items = *resp.JSON200.Networks
		}
		return listPage[fbapi.NetworkBody]{items: items, total: resp.JSON200.Total}, nil
	})
}

// Databases walks every managed database instance in the organization.
func (c *Client) Databases(ctx context.Context, opts ...DatabaseListOption) iter.Seq2[fbapi.DatabaseBody, error] {
	var p fbapi.DatabasesListParams
	for _, o := range opts {
		o(&p)
	}
	return iterate(ctx, func(ctx context.Context, limit, offset int32) (listPage[fbapi.DatabaseBody], error) {
		q := p
		q.Limit, q.Offset = &limit, &offset
		resp, err := c.API.DatabasesListWithResponse(ctx, &q)
		if err != nil {
			return listPage[fbapi.DatabaseBody]{}, err
		}
		if resp.JSON200 == nil {
			return listPage[fbapi.DatabaseBody]{}, errorFrom(resp.StatusCode(), resp.ApplicationproblemJSONDefault, header(resp.HTTPResponse))
		}
		var items []fbapi.DatabaseBody
		if resp.JSON200.Databases != nil {
			items = *resp.JSON200.Databases
		}
		return listPage[fbapi.DatabaseBody]{items: items, total: resp.JSON200.Total}, nil
	})
}

// LoadBalancers walks every load balancer in the organization.
func (c *Client) LoadBalancers(ctx context.Context, opts ...LoadBalancerListOption) iter.Seq2[fbapi.LoadBalancerBody, error] {
	var p fbapi.LoadBalancersListParams
	for _, o := range opts {
		o(&p)
	}
	return iterate(ctx, func(ctx context.Context, limit, offset int32) (listPage[fbapi.LoadBalancerBody], error) {
		q := p
		q.Limit, q.Offset = &limit, &offset
		resp, err := c.API.LoadBalancersListWithResponse(ctx, &q)
		if err != nil {
			return listPage[fbapi.LoadBalancerBody]{}, err
		}
		if resp.JSON200 == nil {
			return listPage[fbapi.LoadBalancerBody]{}, errorFrom(resp.StatusCode(), resp.ApplicationproblemJSONDefault, header(resp.HTTPResponse))
		}
		var items []fbapi.LoadBalancerBody
		if resp.JSON200.LoadBalancers != nil {
			items = *resp.JSON200.LoadBalancers
		}
		return listPage[fbapi.LoadBalancerBody]{items: items, total: resp.JSON200.Total}, nil
	})
}

// Domains walks every domain registration in the organization.
func (c *Client) Domains(ctx context.Context, opts ...DomainListOption) iter.Seq2[fbapi.DomainBody, error] {
	var p fbapi.DomainsListParams
	for _, o := range opts {
		o(&p)
	}
	return iterate(ctx, func(ctx context.Context, limit, offset int32) (listPage[fbapi.DomainBody], error) {
		q := p
		q.Limit, q.Offset = &limit, &offset
		resp, err := c.API.DomainsListWithResponse(ctx, &q)
		if err != nil {
			return listPage[fbapi.DomainBody]{}, err
		}
		if resp.JSON200 == nil {
			return listPage[fbapi.DomainBody]{}, errorFrom(resp.StatusCode(), resp.ApplicationproblemJSONDefault, header(resp.HTTPResponse))
		}
		var items []fbapi.DomainBody
		if resp.JSON200.Domains != nil {
			items = *resp.JSON200.Domains
		}
		return listPage[fbapi.DomainBody]{items: items, total: resp.JSON200.Total}, nil
	})
}

// DomainListOption narrows a domain walk.
type DomainListOption func(*fbapi.DomainsListParams)

// DomainsInState narrows to one lifecycle state, e.g. `active` or `expired`.
func DomainsInState(state string) DomainListOption {
	return func(p *fbapi.DomainsListParams) { p.State = &state }
}

// SearchDomains matches a substring of the name.
func SearchDomains(q string) DomainListOption {
	return func(p *fbapi.DomainsListParams) { p.Q = &q }
}

// There is deliberately no Isos iterator. `GET /v1/isos` takes no limit and
// offset and answers with the whole list, so a paged walk over it would be an
// invention rather than a convenience -- call the generated method.
