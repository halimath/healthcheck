# healthcheck

Minimal framework to implement health checks for go (golang) applications on top
of `net/http`.

![CI Status][ci-img-url] 
[![Go Report Card][go-report-card-img-url]][go-report-card-url] 
[![Package Doc][package-doc-img-url]][package-doc-url] 
[![Releases][release-img-url]][release-url]

`healthcheck` provides a minimal, ultra-simple framework for implementing
liveness and readyness checks for applications. The framework supports HTTP
based probes with configurable readyness checks.

# Installation

This module uses golang modules and can be installed with

```shell
go get github.com/halimath/healthcheck@main
```

It requires go >= 1.18

# Usage

`healthcheck` provides a `Handler` which can be obtained using `healthcheck.New`.
You can add `Check`s to that handler. Checks are executed for readyness probes
but not for liveness probes. `Handler` satisfies `http.Handler` so it can be
mounted to any multiplexer supporting the go standard library handler interface.

See this minimal example

```go
h := healthcheck.New()

h.AddCheck(healthcheck.CheckURL("http://localhost:1234/"))
h.AddCheckFunc(func(ctx context.Context) error {
	// Add your check code here
	return nil
})

http.Handle("/health/", http.StripPrefix("/health", h))

log.Fatal(http.ListenAndServe(":8080", nil))
```

## Options

`healthcheck.New` accepts `Options` that customize the handler`s behavior. Two
factory functions are provided:

* `healthcheck.WithErrorLogger` lets you provide a function to log or otherwise
  treat any `error` received from a readyness check.
* `healthcheck.WithReadynessTimeout` sets a timeout to apply to all readyness
  checks (the default is 10 seconds).

## Bundled checks

### URL

`healthcheck` supports checking URLs via `HTTP GET` using either 
`healthcheck.CheckURL` or `healthcheck.CheckHTTPStatus`. The second offers more
customization. Both checks use a `http.Client` to query a URL and assert the
received status code to be in the range `200` - `299`.

### `sql.DB`

`healthcheck` provides a check that executes a `PingContext` (defined via an
interface) to ping a remote endpoint. As `*sql.DB` implements a `PingContext`
function it can directly be used as a target for the `healthcheck.CheckPinger`
function.

# License

Copyright 2023 Alexander Metzner.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

[http://www.apache.org/licenses/LICENSE-2.0](http://www.apache.org/licenses/LICENSE-2.0)

WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

[ci-img-url]: https://github.com/halimath/healthcheck/workflows/CI/badge.svg
[go-report-card-img-url]: https://goreportcard.com/badge/github.com/halimath/healthcheck
[go-report-card-url]: https://goreportcard.com/report/github.com/halimath/healthcheck
[package-doc-img-url]: https://img.shields.io/badge/GoDoc-Reference-blue.svg
[package-doc-url]: https://pkg.go.dev/github.com/halimath/healthcheck
[release-img-url]: https://img.shields.io/github/v/release/halimath/healthcheck.svg
[release-url]: https://github.com/halimath/healthcheck/releases