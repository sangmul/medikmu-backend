// Package api embeds the OpenAPI contract and serves it with an interactive viewer (Swagger UI).
// It is mounted outside production only; the spec itself is not secret, but there is no reason to
// advertise the surface of a production API.
package api

import (
	_ "embed"
	"net/http"
)

//go:embed openapi.yaml
var spec []byte

// swaggerUI loads a pinned Swagger UI build from a CDN and points it at /openapi.yaml.
const swaggerUI = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Medikmu API</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui.css">
</head>
<body>
  <div id="ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui-bundle.js"></script>
  <script>
    window.ui = SwaggerUIBundle({
      url: "/openapi.yaml",
      dom_id: "#ui",
      persistAuthorization: true,
      // Lets "Try it out" work through ngrok's free-tier interstitial page.
      requestInterceptor: (req) => { req.headers["ngrok-skip-browser-warning"] = "1"; return req; }
    });
  </script>
</body>
</html>`

// Mount registers GET /openapi.yaml and GET /docs. Its signature matches httpx.Mount.
func Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		_, _ = w.Write(spec)
	})
	mux.HandleFunc("GET /docs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(swaggerUI))
	})
}
