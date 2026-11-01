// Package docs renders API documentation from the OpenAPI document:
// interactive HTML (Swagger UI) and Markdown references.
package docs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// HTML wraps the spec in a self-contained Swagger UI page.
// The spec is inlined so the file works from disk (file://) as well as served.
// The note is a short banner subtitle describing where the page comes from.
func HTML(specJSON []byte, title, note string) ([]byte, error) {
	var probe struct {
		OpenAPI string `json:"openapi"`
	}
	_ = json.Unmarshal(specJSON, &probe)
	if title == "" {
		title = "API documentation"
	}
	if note == "" {
		note = "served by <code>merapi doc preview</code>"
	}
	// Inline the spec as a JSON literal safely (< is escaped by json.Marshal).
	specLiteral, err := json.Marshal(string(specJSON))
	if err != nil {
		return nil, err
	}
	page := htmlTemplate
	page = strings.ReplaceAll(page, "{{TITLE}}", htmlEscape(title))
	page = strings.ReplaceAll(page, "{{NOTE}}", note)
	page = strings.ReplaceAll(page, "{{SPEC_JSON}}", string(specLiteral))
	return []byte(page), nil
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

const htmlTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{TITLE}}</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
  <style>
    body { margin: 0; background: #fafafa; }
    #banner { background: #1b1b1b; color: #fff; padding: 10px 20px; font: 14px/1.4 system-ui, sans-serif; }
    #banner code { background: #333; padding: 1px 6px; border-radius: 4px; }
  </style>
</head>
<body>
  <div id="banner"><strong>{{TITLE}}</strong> — {{NOTE}}</div>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.onload = () => {
      const spec = {{SPEC_JSON}};
      window.ui = SwaggerUIBundle({
        spec: JSON.parse(spec),
        dom_id: '#swagger-ui',
        deepLinking: true,
        tryItOutEnabled: true,
        persistAuthorization: true,
        docExpansion: 'list',
        defaultModelsExpandDepth: 1,
        layout: 'BaseLayout'
      });
    };
  </script>
</body>
</html>
`

// Serve starts a live documentation server. The spec is re-read on every
// request so edits show up on refresh.
func Serve(ctx context.Context, specPath, title, addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(specPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		// Convert YAML -> JSON on the fly.
		jsonBytes, err := ToJSON(data)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jsonBytes)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(specPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		jsonBytes, err := ToJSON(data)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		page, err := HTML(jsonBytes, title, "served by <code>merapi doc preview</code> — refresh to reload")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	})

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	fmt.Printf("Serving API docs at http://%s  (Ctrl+C to stop)\n", addr)

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}
}
