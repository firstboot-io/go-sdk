# firstboot-go

The Go client for the Firstboot API.

```
go get github.com/firstboot-io/go-sdk
```

```go
c, err := firstboot.New()          // reads FIRSTBOOT_API_URL and FIRSTBOOT_TOKEN
if err != nil {
        return err
}

resp, err := c.API.ServerCreateWithResponse(ctx, &fbapi.ServerCreateParams{}, fbapi.CreateInputBody{
        Name:  "web-1",
        Plan:  "s1",
        Image: ptr("ubuntu-24-04"),
})
if err != nil {
        return err
}

srv, err := c.WaitForServer(ctx, resp.JSON200.Server.Id)   // until it is running
```

## What this adds over the generated client

`fbapi` is generated from the platform's `openapi.json` and covers every
customer endpoint. This package is the layer a generator cannot write, and it
exists because the hard part of talking to this API is not sending a request.

**A retry that cannot buy a second server.** Every create accepts an
`Idempotency-Key`. This client sets one automatically and, crucially, reuses it
across its own retries: the key is minted once before the first attempt, so a
request whose response was lost is answered with the resource the first attempt
created rather than making another. Without that, retrying a create is how you
end up paying for two machines and managing one.

If your key needs to mean something across process restarts (a Terraform
resource address, a job id), set it yourself and the client leaves it alone:

```go
c.API.ServerCreateWithResponse(ctx, params, body,
        firstboot.WithIdempotencyKey("tf:"+resourceAddress))
```

**Waiting that knows what finished means.** A create answers `202` and converges
in the background. Which state values are terminal differs per resource and
cannot be derived from the schema, so `state.go` carries the table and
`state_test.go` holds it against the generated enums. A value this client does
not recognise counts as *still working*, never as done: an old client waiting a
little too long is a delay, an old client that guessed "ready" hands you a
server that is not.

There is a waiter per thing worth waiting for, and their default budgets differ
because the work does:

| Waiter | Default timeout | What it is waiting for |
| --- | --- | --- |
| `WaitForServer` | 15 min | provisioning; settles on `running` |
| `WaitForVolume` | 15 min | `available`, or `attached` when created onto a server |
| `WaitForLoadBalancer` | 15 min | the data plane; settles on `active` |
| `WaitForDatabase` | 15 min | the appliance; settles on `active` |
| `WaitForServerAction` | 5 min | one power action's own `succeeded`/`failed` |
| `WaitForBuild` | 30 min | an image build; `canceled` returns without an error |
| `WaitForISO` | 60 min | a download from a URL nobody here controls |
| `WaitForDomain` | 20 min | a REGISTRY's answer to a registration |

`WaitForDomain`'s default is nowhere near enough for a transfer, which is
measured in days rather than minutes. Say so with `WithTimeout` rather than
trusting it.

**A rate limit read rather than guessed at.** A refused create carries
`Retry-After`, measured by the platform from the moment a slot actually frees.
The client honours it up to `RetryPolicy.MaxRetryAfter` and falls back to jittered
exponential backoff otherwise.

**Refusals as typed errors.** The API's machine-readable codes become Go
sentinels, so the difference that matters is a comparison rather than a string
match:

```go
if errors.Is(err, firstboot.ErrNoCapacity) { /* waiting can help */ }
if errors.Is(err, firstboot.ErrPlanNotOffered) { /* waiting can never help */ }
```

**Paged lists as iterators.** `Servers`, `Volumes`, `Networks`, `Databases`,
`LoadBalancers` and `Domains`, each fetching a page when it runs out.

```go
for srv, err := range c.Servers(ctx, firstboot.SearchServers("web")) {
        if err != nil {
                return err
        }
        fmt.Println(srv.Name, srv.State)
}
```

They stop on the page that did not fill rather than on `offset >= total`, because
`total` is computed per request: a list that grows while being walked would
otherwise loop past the end. They make no attempt at a consistent snapshot --
nothing in this API offers a cursor -- so a resource created mid-walk may be
missed and one deleted mid-walk may appear twice.

There is deliberately no `Isos` iterator: `GET /v1/isos` takes no `limit` and
`offset` and answers with the whole list, so a paged walk over it would be an
invention rather than a convenience.

**Selecting a set, not a page.** Every groupable kind has a walk that takes the
two grouping filters, applied by the API before paging so a filtered walk
narrows the whole account:

```go
var backends []string
for srv, err := range c.Servers(ctx, firstboot.ServersWithTags("role:web")) {
        if err != nil {
                return err
        }
        backends = append(backends, srv.Id)
}
```

Repeating a tag NARROWS: the filter is a containment test, so two tags mean
both. `…InProject("none")` asks the different question "in no project at all",
which a UUID cannot spell.

## Scope

Only the customer surface. The staff endpoints under `/admin/v1/` authenticate
with a session cookie that an API token can never hold, so generating them would
produce methods whose only possible answer is `401`; they are excluded at
generation time rather than filtered later.

## Regenerating

`fbapi/` is generated and never edited by hand.

```
go generate ./...
```

The directive lives in `generate.go`. It reads
`../platform/api/openapi/openapi.json`, so it needs the platform repository
checked out as a sibling. The spec is deliberately not vendored here: a second
copy is a second answer to "what does the API look like", and the first time
they disagree nobody knows which is wrong.

The generator is a `tool` dependency in `go.mod`, so the version that produced
the checked-in client is recorded rather than remembered. That version matters:
the spec is OpenAPI 3.1 with 289 nullable unions, and oapi-codegen v2.8.0 was
measured against it and handles them.

After regenerating, `go test ./...` is what tells you whether the API added a
state this client does not classify.

## Requirements

Go 1.25 or newer (the list iterators use `iter.Seq2`).

## License

Apache License 2.0. See [LICENSE](LICENSE).
