package firstboot

import (
	"context"
	"iter"

	"github.com/firstboot-io/firstboot-go/fbapi"
)

// Grouping: the project a resource is IN and the tags it WEARS.
//
// The two are one idea in two shapes. A project is 0..1 and exclusive, which is
// how a customer divides their own work; a tag is 0..N and cross-cutting, which
// is how a resource is SELECTED. This file exists for the second: the whole
// reason tags were added to the API is that somebody has to be able to say
// "every server in the web tier" without writing the list down.
//
//	var backends []string
//	for srv, err := range c.Servers(ctx, ServersWithTags("role:web")) {
//	        if err != nil {
//	                return err
//	        }
//	        backends = append(backends, srv.Id.String())
//	}
//
// Every filter is applied BEFORE paging by the API, so a filtered walk narrows
// the whole account rather than one page.
//
// # Why sixteen near-identical functions
//
// The generated parameter structs are eight distinct types that happen to share
// two fields. Go generics reach methods, not fields, so a single `WithTags`
// over all of them would need an interface the generator does not write. The
// alternative was making the CALLER build a params struct, which is the thing
// the iterators exist to avoid. Sixteen three-line functions is the smaller
// cost, and each one is the same three lines.
//
// Repeating a tag NARROWS: the API's filter is a containment test, so naming
// two tags matches what carries both. Passing none is not a filter.

// ---- servers ----

// ServersWithTags narrows to servers carrying EVERY tag given.
func ServersWithTags(tags ...string) ServerListOption {
	return func(p *fbapi.ServersListParams) { p.Tag = &tags }
}

// ---- volumes ----

// VolumesWithTags narrows to volumes carrying EVERY tag given.
func VolumesWithTags(tags ...string) VolumeListOption {
	return func(p *fbapi.VolumeListParams) { p.Tag = &tags }
}

// VolumesInProject narrows to one project, or to the volumes in none of them
// with the literal "none".
func VolumesInProject(id string) VolumeListOption {
	return func(p *fbapi.VolumeListParams) { p.Project = &id }
}

// ---- networks ----

// NetworkListOption narrows a private-network walk.
type NetworkListOption func(*fbapi.NetworksListParams)

// NetworksWithTags narrows to networks carrying EVERY tag given.
func NetworksWithTags(tags ...string) NetworkListOption {
	return func(p *fbapi.NetworksListParams) { p.Tag = &tags }
}

// NetworksInProject narrows to one project, or to "none".
func NetworksInProject(id string) NetworkListOption {
	return func(p *fbapi.NetworksListParams) { p.Project = &id }
}

// ---- databases ----

// DatabaseListOption narrows a managed-database walk.
type DatabaseListOption func(*fbapi.DatabasesListParams)

// DatabasesWithTags narrows to instances carrying EVERY tag given.
func DatabasesWithTags(tags ...string) DatabaseListOption {
	return func(p *fbapi.DatabasesListParams) { p.Tag = &tags }
}

// DatabasesInProject narrows to one project, or to "none".
func DatabasesInProject(id string) DatabaseListOption {
	return func(p *fbapi.DatabasesListParams) { p.Project = &id }
}

// ---- load balancers ----

// LoadBalancerListOption narrows a load-balancer walk.
type LoadBalancerListOption func(*fbapi.LoadBalancersListParams)

// LoadBalancersWithTags narrows to load balancers carrying EVERY tag given.
func LoadBalancersWithTags(tags ...string) LoadBalancerListOption {
	return func(p *fbapi.LoadBalancersListParams) { p.Tag = &tags }
}

// LoadBalancersInProject narrows to one project, or to "none".
func LoadBalancersInProject(id string) LoadBalancerListOption {
	return func(p *fbapi.LoadBalancersListParams) { p.Project = &id }
}

// ---- DNS zones ----

// ZoneListOption narrows a DNS-zone walk.
type ZoneListOption func(*fbapi.DnsZonesListParams)

// ZonesWithTags narrows to zones carrying EVERY tag given.
func ZonesWithTags(tags ...string) ZoneListOption {
	return func(p *fbapi.DnsZonesListParams) { p.Tag = &tags }
}

// ZonesInProject narrows to one project, or to "none".
func ZonesInProject(id string) ZoneListOption {
	return func(p *fbapi.DnsZonesListParams) { p.Project = &id }
}

// Zones walks every DNS zone in the organization.
func (c *Client) Zones(ctx context.Context, opts ...ZoneListOption) iter.Seq2[fbapi.DnsZoneBody, error] {
	var p fbapi.DnsZonesListParams
	for _, o := range opts {
		o(&p)
	}
	return iterate(ctx, func(ctx context.Context, limit, offset int32) (listPage[fbapi.DnsZoneBody], error) {
		q := p
		q.Limit, q.Offset = &limit, &offset
		resp, err := c.API.DnsZonesListWithResponse(ctx, &q)
		if err != nil {
			return listPage[fbapi.DnsZoneBody]{}, err
		}
		if resp.JSON200 == nil {
			return listPage[fbapi.DnsZoneBody]{}, errorFrom(resp.StatusCode(), resp.ApplicationproblemJSONDefault, header(resp.HTTPResponse))
		}
		var items []fbapi.DnsZoneBody
		if resp.JSON200.Zones != nil {
			items = *resp.JSON200.Zones
		}
		return listPage[fbapi.DnsZoneBody]{items: items, total: resp.JSON200.Total}, nil
	})
}

// ---- apps ----

// AppListOption narrows an app walk.
type AppListOption func(*fbapi.AppsListParams)

// AppsWithTags narrows to apps carrying EVERY tag given.
func AppsWithTags(tags ...string) AppListOption {
	return func(p *fbapi.AppsListParams) { p.Tag = &tags }
}

// AppsInProject narrows to one project, or to "none".
func AppsInProject(id string) AppListOption {
	return func(p *fbapi.AppsListParams) { p.Project = &id }
}

// Apps walks every app in the organization.
func (c *Client) Apps(ctx context.Context, opts ...AppListOption) iter.Seq2[fbapi.AppBody, error] {
	var p fbapi.AppsListParams
	for _, o := range opts {
		o(&p)
	}
	return iterate(ctx, func(ctx context.Context, limit, offset int32) (listPage[fbapi.AppBody], error) {
		q := p
		q.Limit, q.Offset = &limit, &offset
		resp, err := c.API.AppsListWithResponse(ctx, &q)
		if err != nil {
			return listPage[fbapi.AppBody]{}, err
		}
		if resp.JSON200 == nil {
			return listPage[fbapi.AppBody]{}, errorFrom(resp.StatusCode(), resp.ApplicationproblemJSONDefault, header(resp.HTTPResponse))
		}
		var items []fbapi.AppBody
		if resp.JSON200.Apps != nil {
			items = *resp.JSON200.Apps
		}
		return listPage[fbapi.AppBody]{items: items, total: resp.JSON200.Total}, nil
	})
}

// ---- domains ----

// DomainsWithTags narrows to domains carrying EVERY tag given.
func DomainsWithTags(tags ...string) DomainListOption {
	return func(p *fbapi.DomainsListParams) { p.Tag = &tags }
}

// DomainsInProject narrows to one project, or to "none".
func DomainsInProject(id string) DomainListOption {
	return func(p *fbapi.DomainsListParams) { p.Project = &id }
}
