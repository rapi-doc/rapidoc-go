package rapidoc

// The handler in a real server.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func serve(t *testing.T, handler http.Handler) func(method, path string, headers ...string) (*http.Response, string) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(method, path string, headers ...string) (*http.Response, string) {
		t.Helper()
		request, err := http.NewRequest(method, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		for index := 0; index+1 < len(headers); index += 2 {
			request.Header.Set(headers[index], headers[index+1])
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response, string(body)
	}
}

func contains(t *testing.T, text string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(text, part) {
			t.Errorf("%q is not in:\n%s", part, text)
		}
	}
}

func status(t *testing.T, response *http.Response, want int) {
	t.Helper()
	if response.StatusCode != want {
		t.Errorf("%s %s: status %d, want %d", response.Request.Method, response.Request.URL.Path, response.StatusCode, want)
	}
}

func TestServesThePageTheBundleAndTheOAuthReturnPage(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/docs/", Handler(Options{SpecURL: "/openapi.json"}))
	mux.Handle("/docs", Handler(Options{SpecURL: "/openapi.json"}))
	get := serve(t, mux)

	response, html := get("GET", "/docs")
	status(t, response, 200)
	if got := response.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("content type %q", got)
	}
	if got := response.Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("cache control %q", got)
	}
	contains(t, html,
		"<title>API Reference</title>",
		`spec-url="/openapi.json"`,
		`oauth-receiver="/docs/oauth-receiver.html"`,
		`<script type="module" src="/docs/rapidoc-min.js"></script>`,
	)
	if _, withSlash := get("GET", "/docs/"); withSlash != html {
		t.Error("/docs/ is not the same page as /docs")
	}

	_, receiver := get("GET", "/docs/oauth-receiver.html")
	contains(t, receiver, "<oauth-receiver></oauth-receiver>", `src="/docs/rapidoc-min.js"`)

	response, script := get("GET", "/docs/rapidoc-min.js")
	status(t, response, 200)
	if got := response.Header.Get("Content-Type"); got != "text/javascript; charset=utf-8" {
		t.Errorf("content type %q", got)
	}
	if len(script) < 100_000 {
		t.Errorf("the bundle has %d bytes", len(script))
	}
	response, _ = get("GET", "/docs/rapidoc-min.js.LICENSE.txt")
	status(t, response, 200)
	if got := response.Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("content type %q", got)
	}

	response, _ = get("GET", "/docs/other.js")
	status(t, response, 404)
	response, _ = get("POST", "/docs")
	status(t, response, 405)
	if got := response.Header.Get("Allow"); got != "GET, HEAD" {
		t.Errorf("allow %q", got)
	}
}

func TestAnswersHeadAndIfNoneMatch(t *testing.T) {
	get := serve(t, Handler(Options{}))

	response, body := get("HEAD", "/docs/rapidoc-min.js")
	status(t, response, 200)
	if body != "" || response.ContentLength < 100_000 {
		t.Errorf("HEAD: %d bytes of body, content length %d", len(body), response.ContentLength)
	}
	etag := response.Header.Get("ETag")
	if !strings.HasPrefix(etag, `"sha384-`) {
		t.Errorf("etag %q", etag)
	}
	if got := response.Header.Get("Cache-Control"); got != "public, max-age=3600" {
		t.Errorf("cache control %q", got)
	}

	response, body = get("GET", "/docs/rapidoc-min.js", "If-None-Match", etag)
	status(t, response, 304)
	if body != "" {
		t.Errorf("304 with %d bytes of body", len(body))
	}

	response, body = get("HEAD", "/docs")
	status(t, response, 200)
	if body != "" {
		t.Error("HEAD of the page has a body")
	}
}

func TestTakesTheOptionsOfEveryIntegration(t *testing.T) {
	get := serve(t, Handler(Options{
		UIPath:  "/reference/",
		SpecURL: "https://example.com/openapi.yaml",
		Title:   "Pets <API>",
		Lang:    "fr",
		CSS:     "rapi-doc{--x:1}",
		Head:    `<meta name="robots" content="noindex">`,
		Attributes: map[string]any{
			"renderStyle":         "focused",
			"allow-try":           false,
			"schema-expand-level": 2,
			"oauth-receiver":      nil,
		},
		Nonce: func(r *http.Request) string { return r.Header.Get("X-Nonce") },
	}))

	_, html := get("GET", "/reference", "X-Nonce", "abc")
	contains(t, html,
		`<html lang="fr">`,
		"<title>Pets &lt;API&gt;</title>",
		`<style nonce="abc">rapi-doc{--x:1}</style>`,
		`src="/reference/rapidoc-min.js" nonce="abc"></script>`,
		`<meta name="robots" content="noindex">`,
		"      allow-try=\"false\"\n      render-style=\"focused\"\n      schema-expand-level=\"2\"\n      spec-url=\"https://example.com/openapi.yaml\"\n",
	)
	if strings.Contains(html, "oauth-receiver=") {
		t.Error("a nil attribute is still written")
	}
	if _, plain := get("GET", "/reference"); strings.Contains(plain, "nonce") {
		t.Error("a request without a nonce gets one")
	}
	response, _ := get("GET", "/docs")
	status(t, response, 404)
}

func TestWritesTheSpecIntoThePage(t *testing.T) {
	const document = `{ "openapi": "3.1.0", "info": { "title": "Inline </script>", "version": "1" }, "paths": {} }`
	inline := `<script type="application/json" slot="rapidoc-spec">{"openapi":"3.1.0","info":{"title":"Inline ` +
		jsonEscape + `003c/script` + jsonEscape + `003e","version":"1"},"paths":{}}</script>`
	type info struct {
		Title   string `json:"title"`
		Version string `json:"version"`
	}
	typed := struct {
		OpenAPI string         `json:"openapi"`
		Info    info           `json:"info"`
		Paths   map[string]any `json:"paths"`
	}{"3.1.0", info{"Inline </script>", "1"}, map[string]any{}}

	for name, spec := range map[string]any{"string": document, "bytes": []byte(document), "struct": typed} {
		_, html := serve(t, Handler(Options{Spec: spec}))("GET", "/docs")
		contains(t, html, inline)
		if strings.Contains(html, "spec-url") {
			t.Errorf("%s: a spec-url nobody set", name)
		}
	}
	if err := (Options{Spec: "openapi: 3.1.0"}).Validate(); err == nil {
		t.Error("YAML as the spec should be refused")
	}
}

func TestLoadsTheBundleFromACDNOrAURLOfYourOwn(t *testing.T) {
	get := serve(t, Handler(Options{Assets: CDN}))
	_, html := get("GET", "/docs")
	pinned := regexp.MustCompile(`<script type="module" src="https://cdn\.jsdelivr\.net/npm/rapidoc@[0-9.]+/dist/rapidoc-min\.js" integrity="sha384-[^"]+" crossorigin="anonymous"></script>`)
	if !pinned.MatchString(html) {
		t.Errorf("no pinned CDN script in:\n%s", html)
	}
	response, _ := get("GET", "/docs/rapidoc-min.js")
	status(t, response, 404)
	_, receiver := get("GET", "/docs/oauth-receiver.html")
	contains(t, receiver, "cdn.jsdelivr.net")

	_, html = serve(t, Handler(Options{Assets: ScriptURL("/static/rapidoc.js", "sha384-abc")}))("GET", "/docs")
	contains(t, html, `src="/static/rapidoc.js" integrity="sha384-abc" crossorigin="anonymous"`)
	if err := (Options{Assets: ScriptURL("", "")}).Validate(); err == nil {
		t.Error("an empty script URL should be refused")
	}
}

func TestWritesThePublicPathWhenAPrefixIsStripped(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", Handler(Options{PublicUIPath: "/api/docs", SpecURL: "/api/openapi.json"})))
	get := serve(t, mux)

	_, html := get("GET", "/api/docs")
	contains(t, html, `src="/api/docs/rapidoc-min.js"`, `oauth-receiver="/api/docs/oauth-receiver.html"`)
	response, _ := get("GET", "/api/docs/rapidoc-min.js")
	status(t, response, 200)
	_, receiver := get("GET", "/api/docs/oauth-receiver.html")
	contains(t, receiver, `src="/api/docs/rapidoc-min.js"`)
}

func TestMiddlewarePassesOtherRequestsOn(t *testing.T) {
	app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "app "+r.URL.Path) })
	get := serve(t, Middleware(Options{UIPath: "/"})(app))

	_, html := get("GET", "/")
	contains(t, html, `src="/rapidoc-min.js"`, `oauth-receiver="/oauth-receiver.html"`)
	response, _ := get("GET", "/rapidoc-min.js")
	status(t, response, 200)
	if _, body := get("GET", "/pets"); body != "app /pets" {
		t.Errorf("the app answered %q", body)
	}
}

func TestRefusesOptionsThatCannotBeUsed(t *testing.T) {
	if err := (Options{Attributes: map[string]any{"on click": "x"}}).Validate(); err == nil {
		t.Error("an invalid attribute name should be refused")
	}
	if err := (Options{SpecURL: "/openapi.json", Assets: CDN}).Validate(); err != nil {
		t.Errorf("valid options refused: %v", err)
	}
	defer func() {
		if recover() == nil {
			t.Error("Handler should panic where Validate returns an error")
		}
	}()
	Handler(Options{Spec: "{"})
}
