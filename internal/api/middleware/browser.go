package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// isBrowserNav reports whether this request is a person opening the URL directly
// in a browser (a top-level navigation) rather than the frontend's fetch() or
// any other API client. Browsers set Sec-Fetch-Mode: navigate on address-bar and
// link navigations; fetch() sets cors|same-origin|no-cors, never navigate. The
// Accept fallback covers the rare client without Sec-Fetch-* headers.
func isBrowserNav(c *gin.Context) bool {
	if c.GetHeader("Sec-Fetch-Mode") == "navigate" {
		return true
	}
	accept := c.GetHeader("Accept")
	return strings.Contains(accept, "text/html") && !strings.Contains(accept, "application/json")
}

// APIError aborts with a JSON error for API clients, but serves a friendly
// on-brand HTML page when a person lands on an API URL in their browser, so a
// typed-in endpoint shows a page instead of raw {"error":...} json. The HTTP
// status is preserved for both.
func APIError(c *gin.Context, status int, msg string) {
	if isBrowserNav(c) {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(status, apiLandingHTML)
		c.Abort()
		return
	}
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}

// NoRoute is the catch-all for unmatched API paths: friendly page for browsers,
// json 404 for everything else.
func NoRoute(c *gin.Context) {
	APIError(c, http.StatusNotFound, "not found")
}

// dark stone ground + muted-gold accent, matching the platform theme.
const apiLandingHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Anvil API</title>
<style>
  :root { color-scheme: dark; }
  html, body { height: 100%; margin: 0; }
  body {
    display: flex; align-items: center; justify-content: center;
    min-height: 100%; padding: 24px; box-sizing: border-box;
    background: #0c0a09; color: #f5f5f4;
    font-family: 'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif;
    -webkit-font-smoothing: antialiased;
  }
  .card { width: 100%; max-width: 420px; text-align: center; }
  .mark {
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    font-size: 12px; letter-spacing: 0.28em; text-transform: uppercase;
    color: #a8935c; margin-bottom: 28px;
  }
  h1 { font-size: 22px; font-weight: 600; margin: 0 0 12px; letter-spacing: -0.01em; }
  p { margin: 0 auto 28px; max-width: 340px; font-size: 14px; line-height: 1.6; color: #a8a29e; }
  a.btn {
    display: inline-block; text-decoration: none; font-size: 13px; font-weight: 500;
    color: #0c0a09; background: #a8935c; padding: 10px 20px; border-radius: 8px;
    transition: background .15s ease;
  }
  a.btn:hover { background: #c4b084; }
</style>
</head>
<body>
  <main class="card">
    <div class="mark">Anvil API</div>
    <h1>Nothing to see here</h1>
    <p>You've reached a backend API endpoint. It's meant to be called by the platform, not opened directly in a browser.</p>
    <a class="btn" href="/">Back to the platform</a>
  </main>
</body>
</html>`
