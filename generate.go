package firstboot

// The directive that makes `go generate ./...` mean something.
//
// It reads the PLATFORM's own spec from the sibling checkout rather than a copy
// kept here, which is the whole reason this file names a path outside the
// module: a vendored spec is a second answer to "what does the API look like",
// and the first time the two disagree nobody knows which is wrong. The cost is
// that generating needs the platform repository beside this one; the CI drift
// job checks both out for exactly that reason.
//
// The generator is a `tool` dependency rather than a `go run …@version`, so the
// version that produced `fbapi/client.gen.go` is recorded in go.mod and go.sum
// instead of in a comment somebody has to trust. That version matters: the spec
// is OpenAPI 3.1 and carries 289 nullable unions, and oapi-codegen v2.8.0 was
// tried against it and handles them. Published comparisons of the generators
// disagree on this point, so the answer here is the measured one.

//go:generate go tool oapi-codegen --config=oapi-codegen.yaml ../platform/api/openapi/openapi.json
