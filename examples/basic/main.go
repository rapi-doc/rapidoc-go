// go run ./examples/basic  →  http://localhost:4408/docs
package main

import (
	"log"
	"net/http"

	rapidoc "github.com/rapi-doc/rapidoc-go"
)

const spec = `{
  "openapi": "3.1.0",
  "info": { "title": "Go example", "version": "1.0.0" },
  "paths": {
    "/hello": {
      "get": {
        "tags": ["Greetings"],
        "summary": "Say hello",
        "parameters": [{ "name": "name", "in": "query", "schema": { "type": "string", "default": "world" } }],
        "responses": {
          "200": {
            "description": "A greeting",
            "content": { "application/json": { "schema": { "type": "object", "properties": { "message": { "type": "string" } } } } }
          }
        }
      }
    }
  }
}`

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(spec))
	})
	mux.HandleFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		if name == "" {
			name = "world"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"Hello, ` + name + `!"}`))
	})

	docs := rapidoc.Handler(rapidoc.Options{
		SpecURL:    "/openapi.json",
		Title:      "Go example - API Reference",
		Attributes: map[string]any{"render-style": "focused", "show-header": false},
	})
	mux.Handle("/docs", docs)
	mux.Handle("/docs/", docs)

	log.Println("http://localhost:4408/docs")
	log.Fatal(http.ListenAndServe("localhost:4408", mux))
}
