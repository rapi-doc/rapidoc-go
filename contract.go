package rapidoc

// The RapiDoc integration contract, version 2, in Go: a port of src/core.js of @rapidoc/embed, same
// function for function. It is checked against the conformance fixtures in testdata/conformance, which
// every port in every language reproduces byte for byte. Change the output only together with core.js and
// its fixtures. Nothing here uses net/http.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ContractVersion is the version of the integration contract this package implements.
const ContractVersion = 2

const (
	defaultUIPath     = "/docs"
	defaultTitle      = "API Reference"
	scriptFile        = "rapidoc-min.js"
	licenseFile       = "rapidoc-min.js.LICENSE.txt"
	oauthReceiverFile = "oauth-receiver.html"

	baseStyle = "html,body{height:100%;margin:0}rapi-doc{height:100vh;width:100%}"
	htmlType  = "text/html; charset=utf-8"

	assetsBundled = "bundled"
	assetsCDN     = "cdn"
	assetsCustom  = "custom"
)

// The start of a JSON escape for a UTF-16 code unit, built so that this file holds no such escape itself.
const jsonEscape = "\\" + "u"

var (
	upperCase        = regexp.MustCompile(`[A-Z]`)
	attributePattern = regexp.MustCompile(`\A[a-z][a-z0-9-]*\z`)
	styleEnd         = regexp.MustCompile(`(?i)</(style)`)
	htmlEscaper      = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	inlineEscaper    = strings.NewReplacer(
		"<", jsonEscape+"003c",
		">", jsonEscape+"003e",
		"&", jsonEscape+"0026",
		string(rune(0x2028)), jsonEscape+"2028",
		string(rune(0x2029)), jsonEscape+"2029",
	)
)

// manifest is integration.json of a rapidoc release: its version, CDN base URL and files.
type manifest struct {
	Version string `json:"version"`
	CDN     string `json:"cdn"`
	Files   map[string]struct {
		Integrity string `json:"integrity"`
	} `json:"files"`
}

// contractAssets says where the browser loads the bundle from.
type contractAssets struct {
	kind      string // "" is bundled
	scriptURL string
	integrity string
}

// contractOptions are the options of the contract. An empty uiPath, title or lang means the default.
type contractOptions struct {
	uiPath     string
	specURL    string
	spec       []byte // the OpenAPI document as JSON text; nil for none
	title      string
	assets     contractAssets
	manifest   *manifest
	attributes map[string]any
	head       string
	css        string
	lang       string
	nonce      string
}

type embedURLs struct {
	page          string
	script        string
	integrity     string
	oauthReceiver string
}

type embedRoute struct {
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	File        string `json:"file,omitempty"`
	ContentType string `json:"contentType"`
}

type attribute struct{ name, value string }

// escapeHTML escapes text for HTML content and double-quoted attribute values.
func escapeHTML(value string) string { return htmlEscaper.Replace(value) }

// inlineJSON is compact JSON, keys in input order, that cannot end the script element it is written into.
func inlineJSON(spec []byte) (string, error) {
	text, err := compactJSON(spec)
	if err != nil {
		return "", err
	}
	return inlineEscaper.Replace(text), nil
}

// normalizePath is a public URL path without a trailing slash; the site root is the empty string.
func normalizePath(path string) string {
	if path == "" {
		path = defaultUIPath
	}
	trimmed := strings.TrimRight(strings.TrimSpace(path), "/")
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "/") {
		return trimmed
	}
	return "/" + trimmed
}

// attributeName turns allowTry into allow-try; names already in kebab-case pass through.
func attributeName(key string) (string, error) {
	name := upperCase.ReplaceAllStringFunc(key, func(letter string) string {
		return "-" + strings.ToLower(letter)
	})
	if !attributePattern.MatchString(name) {
		return "", fmt.Errorf("rapidoc: %q is not a valid attribute name", key)
	}
	return name, nil
}

// attributeValue writes a value as JavaScript's String() does; ok is false for a value that leaves the
// attribute out (nil, or JSON null).
func attributeValue(value any) (text string, ok bool, err error) {
	switch v := value.(type) {
	case nil:
		return "", false, nil
	case string:
		return v, true, nil
	case bool:
		return strconv.FormatBool(v), true, nil
	case json.Number:
		return numberText(v.String()), true, nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true, nil
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32), true, nil
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", v), true, nil
	case json.RawMessage:
		return rawAttributeValue(v)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", false, fmt.Errorf("rapidoc: attribute value: %w", err)
	}
	return rawAttributeValue(encoded)
}

func rawAttributeValue(raw []byte) (string, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", false, nil
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return "", false, fmt.Errorf("rapidoc: attribute value: %w", err)
		}
		return text, true, nil
	}
	text, err := compactJSON(trimmed)
	if err != nil {
		return "", false, fmt.Errorf("rapidoc: attribute value: %w", err)
	}
	return text, true, nil
}

// urls says where the page, the bundle and the OAuth return page are, and how the bundle is loaded.
func (o contractOptions) urls() (embedURLs, error) {
	path := normalizePath(o.uiPath)
	urls := embedURLs{
		page:          path,
		script:        path + "/" + scriptFile,
		oauthReceiver: path + "/" + oauthReceiverFile,
	}
	if path == "" {
		urls.page = "/"
	}
	switch o.assets.kind {
	case "", assetsBundled:
	case assetsCDN:
		if o.manifest == nil || o.manifest.CDN == "" {
			return urls, errors.New(`rapidoc: assets "cdn" needs the integration manifest of rapidoc`)
		}
		file, found := o.manifest.Files[scriptFile]
		if !found {
			return urls, errors.New(`rapidoc: assets "cdn" needs the integration manifest of rapidoc`)
		}
		urls.script = o.manifest.CDN + scriptFile
		urls.integrity = file.Integrity
	case assetsCustom:
		if o.assets.scriptURL == "" {
			return urls, errors.New("rapidoc: the script URL of the assets is required")
		}
		urls.script = o.assets.scriptURL
		urls.integrity = o.assets.integrity
	default:
		return urls, errors.New(`rapidoc: assets must be bundled, cdn or a script URL`)
	}
	return urls, nil
}

// toAttributes returns the attributes of <rapi-doc>, sorted by name. spec-url and oauth-receiver come from
// the embed options; the attributes option adds to them and wins. A nil value leaves an attribute out.
// When two keys name the same attribute (allowTry and allow-try), the one that sorts last wins.
func (o contractOptions) toAttributes() ([]attribute, error) {
	urls, err := o.urls()
	if err != nil {
		return nil, err
	}
	values := map[string]any{"oauth-receiver": urls.oauthReceiver}
	if o.specURL != "" {
		values["spec-url"] = o.specURL
	}
	keys := make([]string, 0, len(o.attributes))
	for key := range o.attributes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		name, err := attributeName(key)
		if err != nil {
			return nil, err
		}
		values[name] = o.attributes[key]
	}

	attributes := make([]attribute, 0, len(values))
	for name, value := range values {
		text, ok, err := attributeValue(value)
		if err != nil {
			return nil, err
		}
		if ok {
			attributes = append(attributes, attribute{name, text})
		}
	}
	sort.Slice(attributes, func(a, b int) bool { return attributes[a].name < attributes[b].name })
	return attributes, nil
}

func scriptTag(urls embedURLs, nonce string) string {
	tag := `<script type="module" src="` + escapeHTML(urls.script) + `"`
	if urls.integrity != "" {
		tag += ` integrity="` + escapeHTML(urls.integrity) + `" crossorigin="anonymous"`
	}
	if nonce != "" {
		tag += ` nonce="` + escapeHTML(nonce) + `"`
	}
	return tag + "></script>"
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// renderHostPage is the HTML page that hosts <rapi-doc>. head and css are written as given: they are the
// developer's own markup, never user input.
func (o contractOptions) renderHostPage() (string, error) {
	urls, err := o.urls()
	if err != nil {
		return "", err
	}
	nonce := ""
	if o.nonce != "" {
		nonce = ` nonce="` + escapeHTML(o.nonce) + `"`
	}
	lines := []string{
		"<!doctype html>",
		`<html lang="` + escapeHTML(orDefault(o.lang, "en")) + `">`,
		"  <head>",
		`    <meta charset="utf-8">`,
		`    <meta name="viewport" content="width=device-width, initial-scale=1">`,
		"    <title>" + escapeHTML(orDefault(o.title, defaultTitle)) + "</title>",
		"    <style" + nonce + ">" + baseStyle + "</style>",
	}
	if o.css != "" {
		lines = append(lines, "    <style"+nonce+">"+styleEnd.ReplaceAllString(o.css, `<\/$1`)+"</style>")
	}
	lines = append(lines, "    "+scriptTag(urls, o.nonce))
	if o.head != "" {
		lines = append(lines, "    "+o.head)
	}
	lines = append(lines, "  </head>", "  <body>")

	attributes, err := o.toAttributes()
	if err != nil {
		return "", err
	}
	hasSpec := o.spec != nil
	if len(attributes) == 0 {
		if hasSpec {
			lines = append(lines, "    <rapi-doc>")
		} else {
			lines = append(lines, "    <rapi-doc></rapi-doc>")
		}
	} else {
		lines = append(lines, "    <rapi-doc")
		for _, item := range attributes {
			lines = append(lines, "      "+item.name+`="`+escapeHTML(item.value)+`"`)
		}
		if hasSpec {
			lines = append(lines, "    >")
		} else {
			lines = append(lines, "    ></rapi-doc>")
		}
	}
	if hasSpec {
		inline, err := inlineJSON(o.spec)
		if err != nil {
			return "", fmt.Errorf("rapidoc: the spec is not valid JSON: %w", err)
		}
		lines = append(lines,
			`      <script type="application/json" slot="rapidoc-spec">`+inline+"</script>",
			"    </rapi-doc>",
		)
	}
	lines = append(lines, "  </body>", "</html>", "")
	return strings.Join(lines, "\n"), nil
}

// renderOAuthReceiverPage is the page the OAuth provider sends the sign-in popup back to. It must be
// served from the same origin as the host page, also when the bundle comes from a CDN.
func (o contractOptions) renderOAuthReceiverPage() (string, error) {
	urls, err := o.urls()
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		"<!doctype html>",
		`<html lang="` + escapeHTML(orDefault(o.lang, "en")) + `">`,
		"  <head>",
		`    <meta charset="utf-8">`,
		"    <title>OAuth sign-in</title>",
		"    " + scriptTag(urls, o.nonce),
		"  </head>",
		"  <body>",
		"    <oauth-receiver></oauth-receiver>",
		"  </body>",
		"</html>",
		"",
	}, "\n"), nil
}

// routes are the routes an integration serves. page and oauth-receiver are rendered; file is a file of
// rapidoc's dist folder, served only when assets are bundled.
func (o contractOptions) routes() ([]embedRoute, error) {
	urls, err := o.urls()
	if err != nil {
		return nil, err
	}
	path := normalizePath(o.uiPath)
	routes := []embedRoute{
		{Path: urls.page, Kind: "page", ContentType: htmlType},
		{Path: urls.oauthReceiver, Kind: "oauth-receiver", ContentType: htmlType},
	}
	if o.assets.kind == "" || o.assets.kind == assetsBundled {
		routes = append(routes,
			embedRoute{Path: urls.script, Kind: "file", File: scriptFile, ContentType: "text/javascript; charset=utf-8"},
			embedRoute{Path: path + "/" + licenseFile, Kind: "file", File: licenseFile, ContentType: "text/plain; charset=utf-8"},
		)
	}
	return routes, nil
}

// numberText writes a whole number given with a fraction (2.0) without it, as JavaScript does.
func numberText(raw string) string {
	if !strings.ContainsAny(raw, ".eE") {
		return raw
	}
	number, err := strconv.ParseFloat(raw, 64)
	if err != nil || number != float64(int64(number)) || number >= 1e15 || number <= -1e15 {
		return raw
	}
	return strconv.FormatInt(int64(number), 10)
}

// compactJSON rewrites JSON text without whitespace, as JavaScript's JSON.stringify writes it: keys in
// input order and non-ASCII characters as they are. (encoding/json escapes <, > and & and sorts map keys.)
func compactJSON(data []byte) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var text strings.Builder
	if err := writeJSONValue(&text, decoder); err != nil {
		return "", err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return "", errors.New("unexpected data after the JSON value")
	}
	return text.String(), nil
}

func writeJSONValue(text *strings.Builder, decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch value := token.(type) {
	case json.Delim:
		if value == '{' {
			text.WriteByte('{')
			for first := true; decoder.More(); first = false {
				if !first {
					text.WriteByte(',')
				}
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				writeJSONString(text, key.(string))
				text.WriteByte(':')
				if err := writeJSONValue(text, decoder); err != nil {
					return err
				}
			}
			text.WriteByte('}')
		} else {
			text.WriteByte('[')
			for first := true; decoder.More(); first = false {
				if !first {
					text.WriteByte(',')
				}
				if err := writeJSONValue(text, decoder); err != nil {
					return err
				}
			}
			text.WriteByte(']')
		}
		// the closing delimiter
		if _, err := decoder.Token(); err != nil {
			return err
		}
	case string:
		writeJSONString(text, value)
	case json.Number:
		text.WriteString(numberText(value.String()))
	case bool:
		text.WriteString(strconv.FormatBool(value))
	case nil:
		text.WriteString("null")
	}
	return nil
}

func writeJSONString(text *strings.Builder, value string) {
	text.WriteByte('"')
	for _, character := range value {
		switch character {
		case '"':
			text.WriteString(`\"`)
		case '\\':
			text.WriteString(`\\`)
		case '\b':
			text.WriteString(`\b`)
		case '\f':
			text.WriteString(`\f`)
		case '\n':
			text.WriteString(`\n`)
		case '\r':
			text.WriteString(`\r`)
		case '\t':
			text.WriteString(`\t`)
		default:
			if character < ' ' {
				text.WriteString(jsonEscape)
				text.WriteString(fmt.Sprintf("%04x", character))
			} else {
				text.WriteRune(character)
			}
		}
	}
	text.WriteByte('"')
}
