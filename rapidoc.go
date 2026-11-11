// Package rapidoc serves a RapiDoc API reference from a Go web app: an interactive page for an OpenAPI
// document, with a console to try requests.
//
//	mux := http.NewServeMux()
//	mux.Handle("/docs/", rapidoc.Handler(rapidoc.Options{SpecURL: "/openapi.json"}))
//
// The handler is a plain net/http handler with no dependencies, so it works with the standard mux, chi
// and gorilla directly, and with Gin, Echo and Fiber through their net/http adapters. The RapiDoc bundle
// is inside the package (go:embed); nothing is loaded from another site unless Assets says so.
//
// The page, its routes and its HTML follow the RapiDoc integration contract, the same one the
// integrations for other languages implement: https://rapidocui.com/docs/get-started/build-an-integration/
package rapidoc

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

//go:embed assets/rapidoc-min.js assets/rapidoc-min.js.LICENSE.txt assets/integration.json
var assets embed.FS

// bundle is integration.json of the rapidoc release inside this package.
var bundle = func() manifest {
	var parsed manifest
	data, err := assets.ReadFile("assets/integration.json")
	if err == nil {
		err = json.Unmarshal(data, &parsed)
	}
	if err != nil {
		panic("rapidoc: assets/integration.json: " + err.Error())
	}
	return parsed
}()

// BundleVersion is the version of RapiDoc (the npm package rapidoc) that this package carries and serves.
func BundleVersion() string { return bundle.Version }

// Assets says where the browser loads the RapiDoc bundle from: [Bundled] (the zero value), [CDN] or
// [ScriptURL].
type Assets struct {
	kind      string
	scriptURL string
	integrity string
}

var (
	// Bundled has the app serve rapidoc-min.js, which is inside this package. The default.
	Bundled = Assets{}
	// CDN has the browser load the same version from jsDelivr, checked against its SRI hash.
	CDN = Assets{kind: assetsCDN}
)

// ScriptURL has the browser load the bundle from a URL of your own, with an optional SRI hash.
func ScriptURL(scriptURL, integrity string) Assets {
	return Assets{kind: assetsCustom, scriptURL: scriptURL, integrity: integrity}
}

// Options are the options of every RapiDoc integration, in Go naming.
type Options struct {
	// UIPath is the URL path the reference page is served at: the path the handler is mounted on.
	// Default "/docs"; "/" is the site root.
	UIPath string

	// PublicUIPath is UIPath as the browser sees it. Set it only when a prefix is removed before the
	// request reaches the handler, by http.StripPrefix or a reverse proxy: with the handler at "/docs"
	// behind a proxy that strips "/api", it is "/api/docs".
	PublicUIPath string

	// SpecURL is the URL of the OpenAPI document, which the browser fetches when the page opens.
	SpecURL string

	// Spec is the OpenAPI document itself: JSON as []byte, json.RawMessage or string, or any value that
	// encoding/json marshals to it. It is written into the page, so the browser makes no request for it.
	// With both, Spec is shown and SpecURL is the download link. (YAML is not read; use SpecURL for it.)
	Spec any

	// Title is the page title. Default "API Reference".
	Title string

	// Assets says where the browser loads the bundle from: Bundled (the default), CDN or ScriptURL(...).
	Assets Assets

	// Attributes are attributes of <rapi-doc> by name, in kebab-case or camelCase:
	// {"render-style": "focused", "allowTry": false}. They win over the package's own, and a nil value
	// removes one. See https://rapidocui.com/api/.
	Attributes map[string]any

	// Head is HTML added to the end of <head>, as given.
	Head string

	// CSS is added in a <style> element, as given.
	CSS string

	// Lang is the language of the page. Default "en".
	Lang string

	// Nonce reads the request's Content-Security-Policy nonce, which is set on every <script> and <style>.
	Nonce func(*http.Request) string
}

type page struct {
	contentType string
	render      func(nonce string) (string, error)
	static      []byte // the page without a nonce, rendered once
}

type file struct {
	contentType string
	content     []byte
	etag        string
}

type handler struct {
	pages map[string]*page
	files map[string]*file
	nonce func(*http.Request) string
	next  http.Handler // answers requests for other paths; nil for 404
}

// Validate reports options that cannot be used: an attribute name that is not valid, a Spec that is not
// JSON, a ScriptURL without a URL. [Handler] and [Middleware] panic on such options, as http.ServeMux
// does on a pattern it cannot use; call Validate first when the options come from outside the program,
// such as a spec read from a file at startup.
func (opts Options) Validate() error {
	_, err := newHandler(opts, nil)
	return err
}

// Handler returns a handler that serves the reference at opts.UIPath, with the OAuth return page and,
// unless the bundle comes from a CDN, rapidoc-min.js under it. It answers GET and HEAD; any other path is
// a 404. It panics on options that cannot be used; see [Options.Validate].
func Handler(opts Options) http.Handler {
	created, err := newHandler(opts, nil)
	if err != nil {
		panic(err)
	}
	return created
}

// Middleware serves the reference like [Handler] and passes every other request to the next handler, for
// routers that take middleware (chi's Use) and for a reference at the site root. It panics on options
// that cannot be used; see [Options.Validate].
func Middleware(opts Options) func(http.Handler) http.Handler {
	if _, err := newHandler(opts, nil); err != nil {
		panic(err)
	}
	return func(next http.Handler) http.Handler {
		created, _ := newHandler(opts, next)
		return created
	}
}

func newHandler(opts Options, next http.Handler) (*handler, error) {
	spec, err := specJSON(opts.Spec)
	if err != nil {
		return nil, err
	}
	routed := contractOptions{
		uiPath:   opts.UIPath,
		assets:   contractAssets(opts.Assets),
		manifest: &bundle,
	}
	if routed.uiPath == "" {
		routed.uiPath = defaultUIPath
	}
	// The pages are written with the path the browser uses; requests are matched on the path the
	// handler sees. They differ only when a prefix is stripped on the way.
	written := routed
	if opts.PublicUIPath != "" {
		written.uiPath = opts.PublicUIPath
	}
	written.specURL = opts.SpecURL
	written.spec = spec
	written.title = opts.Title
	written.attributes = opts.Attributes
	written.head = opts.Head
	written.css = opts.CSS
	written.lang = opts.Lang

	routes, err := routed.routes()
	if err != nil {
		return nil, err
	}
	created := &handler{pages: map[string]*page{}, files: map[string]*file{}, nonce: opts.Nonce, next: next}
	for _, route := range routes {
		if route.Kind == "file" {
			content, err := assets.ReadFile("assets/" + route.File)
			if err != nil {
				return nil, fmt.Errorf("rapidoc: %s is missing from the package: %w", route.File, err)
			}
			// The SRI hash names the content, so it is also the ETag
			etag := route.File
			if entry, found := bundle.Files[route.File]; found && entry.Integrity != "" {
				etag = entry.Integrity
			}
			created.files[route.Path] = &file{contentType: route.ContentType, content: content, etag: `"` + etag + `"`}
			continue
		}

		render := func(nonce string) (string, error) {
			options := written
			options.nonce = nonce
			return options.renderHostPage()
		}
		if route.Kind == "oauth-receiver" {
			render = func(nonce string) (string, error) {
				options := written
				options.nonce = nonce
				return options.renderOAuthReceiverPage()
			}
		}
		// Also checks the options now instead of on the first request
		html, err := render("")
		if err != nil {
			return nil, err
		}
		entry := &page{contentType: route.ContentType, render: render, static: []byte(html)}
		created.pages[route.Path] = entry
		if route.Kind == "page" && route.Path != "/" {
			created.pages[route.Path+"/"] = entry
		}
	}
	return created, nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	entry, isPage := h.pages[path]
	asset, isFile := h.files[path]
	if !isPage && !isFile {
		if h.next != nil {
			h.next.ServeHTTP(w, r)
		} else {
			http.NotFound(w, r)
		}
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	if isFile {
		w.Header().Set("ETag", asset.etag)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		if r.Header.Get("If-None-Match") == asset.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		write(w, r, asset.contentType, asset.content)
		return
	}

	body := entry.static
	if h.nonce != nil {
		if nonce := h.nonce(r); nonce != "" {
			html, err := entry.render(nonce)
			if err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			body = []byte(html)
		}
	}
	w.Header().Set("Cache-Control", "no-cache")
	write(w, r, entry.contentType, body)
}

func write(w http.ResponseWriter, r *http.Request, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}

// specJSON is the Spec option as JSON text, or nil for none.
func specJSON(spec any) ([]byte, error) {
	var data []byte
	switch value := spec.(type) {
	case nil:
		return nil, nil
	case []byte:
		data = value
	case json.RawMessage:
		data = value
	case string:
		data = []byte(value)
	default:
		encoded, err := json.Marshal(spec)
		if err != nil {
			return nil, fmt.Errorf("rapidoc: the spec cannot be written as JSON: %w", err)
		}
		data = encoded
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	if _, err := compactJSON(data); err != nil {
		return nil, fmt.Errorf("rapidoc: the spec is not valid JSON: %w", err)
	}
	return data, nil
}
