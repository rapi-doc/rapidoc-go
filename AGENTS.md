# AGENTS.md — `rapidoc-go` (`github.com/rapi-doc/rapidoc-go`)

## 1. Scope & Identity

RapiDoc for Go: `rapidoc.Handler(options)` is a `net/http` handler that serves the API reference, the OAuth return page and the bundle. It is a port of the integration contract of `@rapidoc/embed` (in the RapiDoc monorepo, `packages/rapidoc-embed`), checked with that package's conformance fixtures.

- **One module, one package, no dependencies.** Standard library only, Go 1.22 or later. A framework-specific helper (Huma, Gin) would be a separate module, so this one never adds a framework to a user's `go.mod`.
- **Its own repository**, unlike the Python and .NET integrations: a Go import path is the repository path, and `go get` runs no build step, so the bundle and the fixtures are committed here.
- **A release is a git tag** `vX.Y.Z` on `main`; there is no registry to publish to.

| File | Role |
| :--- | :--- |
| `contract.go` | The contract in Go: a port of `src/core.js` of `@rapidoc/embed`, same function for function. No `net/http` |
| `rapidoc.go` | The public API: `Options` (and `Options.Validate`), `Handler`, `Middleware`, `Assets` (`Bundled`, `CDN`, `ScriptURL`), `BundleVersion`; the embedded files |
| `assets/` | One rapidoc release: `rapidoc-min.js`, its license file, `integration.json`. Committed; embedded with `go:embed` |
| `RAPIDOC_VERSION` | The rapidoc release in `assets/`; a test fails when the two disagree |
| `testdata/conformance/` | The fixtures and README of `@rapidoc/embed`, copied in |
| `scripts/sync.sh` | Refreshes `assets/` and `testdata/conformance/` from npm, or from a monorepo checkout (`--from`) |
| `conformance_test.go`, `rapidoc_test.go` | The shared fixtures; the handler on a real server |
| `examples/basic/` | A standard-library app (port 4408) |

---

## 2. Boundaries and Invariants

- **`contract.go` and `core.js` change together.** A change to the contract's output is made in `@rapidoc/embed` first (new contract version, regenerated fixtures), then ported here until `TestConformanceFixtures` passes. Never edit a fixture; run `scripts/sync.sh`.
- **JSON is written by `compactJSON`**, not `encoding/json`: that one escapes `<`, `>` and `&` and sorts map keys, and the fixtures need what JavaScript's `JSON.stringify` writes, with keys in input order.
- **No `\u` escape sequences in the sources.** Some editing tools turn them into the character itself; build them (`jsonEscape + "003c"`, `string(rune(0x2028))`) as `contract.go` does.
- **The three asset files come from one rapidoc release**; `integration.json` carries the bundle's SRI hash, which is also the ETag.
- **Everything not exported stays unexported** until someone needs it: an exported name cannot be taken back.
- **One constructor.** `Handler` (and `Middleware`) panic on options that cannot be used; `Options.Validate` returns the error. Do not add a second constructor that differs only in how it fails.
- **Requests are matched on `UIPath`; pages are written with `PublicUIPath`.** They differ only when a prefix is stripped before the handler.
- **`gofmt` and `go vet` clean**; CI checks both.

---

## 3. Commands

| Command | Purpose |
| :--- | :--- |
| `go test ./...` | Conformance fixtures and handler tests |
| `go run ./examples/basic` | Run the example at http://localhost:4408/docs |
| `scripts/sync.sh` | Refresh the bundle (version in `RAPIDOC_VERSION`) and the fixtures from npm |
| `scripts/sync.sh 12.3.0` | Move to another rapidoc release |
| `gofmt -l . && go vet ./...` | What CI checks besides the tests |

**Releasing**: the steps are in `README.md`, "Releasing (for maintainers)".
