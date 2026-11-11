# rapidoc-go

Serves a [RapiDoc](https://rapidocui.com) API reference from a Go web app: an interactive page for your OpenAPI document, with a console to try requests.

```bash
go get github.com/rapi-doc/rapidoc-go
```

```go
package main

import (
	"net/http"

	rapidoc "github.com/rapi-doc/rapidoc-go"
)

func main() {
	mux := http.NewServeMux()

	docs := rapidoc.Handler(rapidoc.Options{SpecURL: "/openapi.json"})
	mux.Handle("/docs", docs)
	mux.Handle("/docs/", docs)

	http.ListenAndServe(":8080", mux)
}
```

Open `/docs`. Nothing is loaded from another site: the package carries the RapiDoc bundle, pinned to one release.

It is a plain `net/http` handler with no dependencies, so it works wherever one does. It does not generate the OpenAPI document; point it at the one your app already has, from Huma, swaggo, ogen, oapi-codegen or a file you wrote.

## Options

```go
rapidoc.Handler(rapidoc.Options{
	UIPath:     "/reference",
	SpecURL:    "/openapi.json",
	Title:      "Pets API",
	Attributes: map[string]any{"render-style": "focused", "allow-try": false},
})
```

| Option | Default | |
| :--- | :--- | :--- |
| `UIPath` | `/docs` | URL path the reference page is served at: the path you mount the handler on. `/` is the site root |
| `SpecURL` | | URL of your OpenAPI document, which the browser fetches when the page opens. Use it when the app already serves the document |
| `Spec` | | The OpenAPI document itself: JSON as `[]byte`, `json.RawMessage` or `string`, or a value that `encoding/json` marshals to it. It is written into the page, so the browser makes no request for it. With both, `Spec` is shown and `SpecURL` is the download link. YAML is not read here; serve it and use `SpecURL` |
| `Title` | `API Reference` | Page title |
| `Assets` | `rapidoc.Bundled` | `Bundled`: the app serves `rapidoc-min.js`. `rapidoc.CDN`: the browser loads the same version from jsDelivr, checked against its SRI hash. `rapidoc.ScriptURL(url, integrity)`: a URL of your own |
| `Attributes` | | [Attributes of `<rapi-doc>`](https://rapidocui.com/api/), in kebab-case or camelCase. They win over the package's own, and `nil` removes one |
| `Head` | | HTML added to the end of `<head>`, as given |
| `CSS` | | CSS added in a `<style>` element, as given |
| `Lang` | `en` | Language of the page |
| `Nonce` | | `func(*http.Request) string` that reads the request's Content-Security-Policy nonce, which is then set on every `<script>` and `<style>` |
| `PublicUIPath` | `UIPath` | `UIPath` as the browser sees it, when `http.StripPrefix` or a reverse proxy removes a prefix before the request reaches the handler |

These are the options of every RapiDoc integration, in Go naming.

`Handler` panics on options that cannot be used (an attribute name that is not valid, a `Spec` that is not JSON), as `http.ServeMux` does on a pattern it cannot use. When the options come from outside the program, such as a spec read from a file at startup, check them first:

```go
if err := options.Validate(); err != nil {
	log.Printf("API docs disabled: %v", err)
} else {
	mux.Handle("/docs/", rapidoc.Handler(options))
}
```

`Middleware(options)` serves the reference and passes every other request to the next handler.

## With a router or framework

The handler matches the full request path, so mount it without stripping the prefix.

```go
// chi
r.Mount("/docs", rapidoc.Handler(rapidoc.Options{SpecURL: "/openapi.json"}))

// Gin
docs := gin.WrapH(rapidoc.Handler(rapidoc.Options{SpecURL: "/openapi.json"}))
router.GET("/docs", docs)
router.GET("/docs/*file", docs)

// Echo
docs := echo.WrapHandler(rapidoc.Handler(rapidoc.Options{SpecURL: "/openapi.json"}))
e.GET("/docs", docs)
e.GET("/docs/*", docs)

// Fiber (github.com/gofiber/fiber/v2/middleware/adaptor)
app.Use("/docs", adaptor.HTTPHandler(rapidoc.Handler(rapidoc.Options{SpecURL: "/openapi.json"})))
```

### Huma

[Huma](https://huma.rocks) generates the OpenAPI document and serves it at `/openapi.json`. Turn off its built-in docs page and mount RapiDoc on the router underneath:

```go
router := chi.NewMux()

config := huma.DefaultConfig("Pets API", "1.0.0")
config.DocsPath = "" // RapiDoc serves /docs
api := humachi.New(router, config)

router.Mount("/docs", rapidoc.Handler(rapidoc.Options{
	SpecURL: "/openapi.json",
	Title:   "Pets API",
}))
```

## Notes

- The handler answers `GET` and `HEAD` on the page, `{UIPath}/oauth-receiver.html`, `{UIPath}/rapidoc-min.js` and its license file. Any other path is a 404 (or goes to the next handler, with `Middleware`).
- OAuth sign-in returns to `{UIPath}/oauth-receiver.html`, served on your own origin, also with `rapidoc.CDN`.
- The bundle is sent with an `ETag`, and `rapidoc-min.js.LICENSE.txt` next to it lists the libraries inside it.
- `rapidoc.BundleVersion()` is the RapiDoc release the package carries (the file `RAPIDOC_VERSION`).
- The page, its routes and its HTML follow the [RapiDoc integration contract](https://rapidocui.com/docs/get-started/build-an-integration/), which the integrations for JavaScript, Python and .NET also implement; `testdata/conformance` holds its fixtures.

Go 1.22 or later. MIT.

## Releasing (for maintainers)

A release is a git tag; the Go module proxy picks it up, and there is no registry account or upload.

1. **Choose the bundle.** To ship a newer RapiDoc, run `scripts/sync.sh <version>` (for example `scripts/sync.sh 12.3.0`). It replaces `assets/`, the fixtures in `testdata/conformance/` and `RAPIDOC_VERSION`. Skip this for a release that only changes the Go code.
2. **Check.** `go test ./...`
3. **Merge to `main`** and wait for the Test workflow.
4. **Tag and push:**

   ```bash
   git tag v0.1.1
   git push origin v0.1.1
   ```

5. **Verify.** `go get github.com/rapi-doc/rapidoc-go@v0.1.1` in a new module, and check https://pkg.go.dev/github.com/rapi-doc/rapidoc-go (the first request for a new version makes the proxy fetch it).

Which number to raise: a fix or a newer bundle is a patch (v0.1.1); a new option is a minor (v0.2.0). While the version is v0.x, a change that breaks existing code is also a minor, and is called out in the release notes. A tag cannot be moved or reused once the proxy has seen it, so fix a bad release with a new tag.
