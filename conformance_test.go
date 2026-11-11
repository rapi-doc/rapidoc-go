package rapidoc

// Runs the conformance fixtures of @rapidoc/embed against the Go port of the contract.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const fixtures = "testdata/conformance/fixtures"

// fixtureInput is input.json of a fixture. Values whose key order or exact form matters stay raw.
type fixtureInput struct {
	Page    string `json:"page"`
	Options struct {
		UIPath   *string                    `json:"uiPath"`
		Path     *string                    `json:"path"`
		SpecURL  string                     `json:"specUrl"`
		Spec     json.RawMessage            `json:"spec"`
		Title    string                     `json:"title"`
		Assets   json.RawMessage            `json:"assets"`
		Manifest *manifest                  `json:"manifest"`
		Options  map[string]json.RawMessage `json:"options"`
		Head     string                     `json:"head"`
		CSS      string                     `json:"css"`
		Lang     string                     `json:"lang"`
		Nonce    string                     `json:"nonce"`
	} `json:"options"`
}

func (input fixtureInput) contractOptions(t *testing.T) contractOptions {
	t.Helper()
	from := input.Options
	options := contractOptions{
		specURL:  from.SpecURL,
		title:    from.Title,
		manifest: from.Manifest,
		head:     from.Head,
		css:      from.CSS,
		lang:     from.Lang,
		nonce:    from.Nonce,
	}
	// uiPath, or path, its name in contract version 1; "/" (the root) is not the same as absent
	switch {
	case from.UIPath != nil:
		options.uiPath = *from.UIPath
	case from.Path != nil:
		options.uiPath = *from.Path
	}
	if len(from.Spec) > 0 && string(from.Spec) != "null" {
		options.spec = from.Spec
	}
	if from.Options != nil {
		options.attributes = map[string]any{}
		for key, value := range from.Options {
			options.attributes[key] = value
		}
	}
	if len(from.Assets) > 0 {
		var kind string
		var custom struct {
			ScriptURL string `json:"scriptUrl"`
			Integrity string `json:"integrity"`
		}
		if err := json.Unmarshal(from.Assets, &kind); err == nil {
			options.assets = contractAssets{kind: kind}
		} else if err := json.Unmarshal(from.Assets, &custom); err == nil {
			options.assets = contractAssets{kind: assetsCustom, scriptURL: custom.ScriptURL, integrity: custom.Integrity}
		} else {
			t.Fatalf("assets: %v", err)
		}
	}
	return options
}

func TestConformanceFixtures(t *testing.T) {
	folders, err := os.ReadDir(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	cases := 0
	for _, folder := range folders {
		name := folder.Name()
		data, err := os.ReadFile(filepath.Join(fixtures, name, "input.json"))
		if err != nil {
			continue
		}
		cases++
		t.Run(name, func(t *testing.T) {
			var input fixtureInput
			if err := json.Unmarshal(data, &input); err != nil {
				t.Fatal(err)
			}
			options := input.contractOptions(t)

			if input.Page == "routes" {
				expected, err := os.ReadFile(filepath.Join(fixtures, name, "expected.json"))
				if err != nil {
					t.Fatal(err)
				}
				routes, err := options.routes()
				if err != nil {
					t.Fatal(err)
				}
				actual, _ := json.Marshal(routes)
				var want, got any
				if err := json.Unmarshal(expected, &want); err != nil {
					t.Fatal(err)
				}
				_ = json.Unmarshal(actual, &got)
				if !reflect.DeepEqual(want, got) {
					t.Errorf("routes differ:\n got %s\nwant %s", actual, expected)
				}
				return
			}

			expected, err := os.ReadFile(filepath.Join(fixtures, name, "expected.html"))
			if err != nil {
				t.Fatal(err)
			}
			var html string
			if input.Page == "host" {
				html, err = options.renderHostPage()
			} else {
				html, err = options.renderOAuthReceiverPage()
			}
			if err != nil {
				t.Fatal(err)
			}
			if html != string(expected) {
				t.Errorf("page differs:\n--- got\n%s\n--- want\n%s", html, expected)
			}
		})
	}
	if cases < 15 {
		t.Fatalf("only %d conformance fixtures in %s", cases, fixtures)
	}
}

func TestInlineJSONCannotEndItsScriptElement(t *testing.T) {
	lineSeparator, paragraphSeparator := string(rune(0x2028)), string(rune(0x2029))
	name := "Zo" + string(rune(0xEB)) + " " + string(rune(0x1F600))
	spec := map[string]any{"info": map[string]any{
		"title": "</script><b>&",
		"note":  "a" + lineSeparator + "b" + paragraphSeparator + "\ttab \"quoted\" back\\slash",
		"name":  name,
	}}
	encoded, _ := json.Marshal(spec)
	text, err := inlineJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"<", ">", "&", lineSeparator, paragraphSeparator} {
		if strings.Contains(text, unwanted) {
			t.Errorf("%q is in %s", unwanted, text)
		}
	}
	if !strings.Contains(text, name) {
		t.Errorf("other non-ASCII characters are written as they are: %s", text)
	}
	var back any
	if err := json.Unmarshal([]byte(text), &back); err != nil || !reflect.DeepEqual(back, spec) {
		t.Errorf("does not read back as the same document: %v %s", err, text)
	}
}

func TestCompactJSONWritesWhatJavaScriptDoes(t *testing.T) {
	cases := map[string]string{
		`{ "b" : 1, "a" : [ 1.0, 2.50, -3, 1e3, true, null, "x" ] }`: `{"b":1,"a":[1,2.50,-3,1000,true,null,"x"]}`,
		`"a\/b ` + jsonEscape + `00e9 ` + jsonEscape + `0001"`:       `"a/b ` + string(rune(0xE9)) + ` ` + jsonEscape + `0001"`,
		`{}`: `{}`,
		`[]`: `[]`,
	}
	for input, want := range cases {
		got, err := compactJSON([]byte(input))
		if err != nil || got != want {
			t.Errorf("compactJSON(%s) = %s, %v; want %s", input, got, err, want)
		}
	}
	for _, invalid := range []string{``, `{`, `{"a":1} x`, `nope`} {
		if _, err := compactJSON([]byte(invalid)); err == nil {
			t.Errorf("compactJSON(%q) should fail", invalid)
		}
	}
}

func TestAttributeValuesAndNames(t *testing.T) {
	options := contractOptions{attributes: map[string]any{
		"a":              2.0,
		"b":              1.5,
		"c":              true,
		"d":              []any{1, "x", nil},
		"e":              10,
		"f":              json.Number("3.0"),
		"renderStyle":    "focused",
		"oauth-receiver": nil,
	}}
	attributes, err := options.toAttributes()
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, item := range attributes {
		got = append(got, item.name+"="+item.value)
	}
	want := []string{"a=2", "b=1.5", "c=true", `d=[1,"x",null]`, "e=10", "f=3", "render-style=focused"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	for _, invalid := range []string{"on click", "x\n", "", "1a"} {
		if _, err := attributeName(invalid); err == nil {
			t.Errorf("attributeName(%q) should fail", invalid)
		}
	}
}

func TestTheBundleIsTheReleaseNamedInRapidocVersion(t *testing.T) {
	pinned, err := os.ReadFile("RAPIDOC_VERSION")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.TrimSpace(string(pinned)); Version() != want {
		t.Errorf("assets hold rapidoc %s, RAPIDOC_VERSION says %s; run scripts/sync.sh", Version(), want)
	}
	if bundle.Files[scriptFile].Integrity == "" || bundle.CDN == "" {
		t.Error("integration.json has no SRI hash or CDN URL for the bundle")
	}
}
