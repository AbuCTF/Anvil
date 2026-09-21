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

// landingCSP loosens the global `default-src 'self'` just enough for this one
// self-contained page: its inline style + theme script, and the two web fonts
// the site uses (JetBrains Mono / Inter) from Google Fonts. API responses keep
// the strict policy.
const landingCSP = "default-src 'none'; style-src 'unsafe-inline' https://fonts.googleapis.com; font-src https://fonts.gstatic.com; script-src 'unsafe-inline'"

// APIError aborts with a JSON error for API clients, but serves a friendly
// on-brand HTML page when a person lands on an API URL in their browser, so a
// typed-in endpoint shows a page instead of raw {"error":...} json. The HTTP
// status is preserved for both.
func APIError(c *gin.Context, status int, msg string) {
	if isBrowserNav(c) {
		c.Header("Content-Security-Policy", landingCSP)
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

// Mirrors the platform's own shell: stone-950 ground, JetBrains Mono body with
// an Inter uppercase label, dark by default and light when the app's saved
// `theme` says so (same localStorage key + tokens as web/src/app.css).
const apiLandingHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Anvil API</title>
<script>
  try {
    if (localStorage.getItem('theme') === 'light')
      document.documentElement.setAttribute('data-theme', 'light');
  } catch (e) {}
</script>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Inter:wght@600&family=JetBrains+Mono:wght@400;500;600&display=swap">
<style>
  :root {
    --ground: 12 10 9;
    --text: 245 245 244;
    --muted: 168 162 158;
    --border: 41 37 36;
    --accent: 168 147 92;
    color-scheme: dark;
  }
  :root[data-theme="light"] {
    --ground: 250 249 246;
    --text: 28 25 23;
    --muted: 92 87 81;
    --border: 214 209 200;
    --accent: 140 120 71;
    color-scheme: light;
  }
  html, body { height: 100%; margin: 0; }
  body {
    display: flex; align-items: center; justify-content: center;
    min-height: 100%; padding: 24px; box-sizing: border-box;
    background: rgb(var(--ground)); color: rgb(var(--text));
    font-family: "JetBrains Mono", "SF Mono", "Consolas", "Menlo", monospace;
    -webkit-font-smoothing: antialiased;
  }
  .card { width: 100%; max-width: 440px; text-align: center; }
  .mark {
    font-family: "Inter", -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    font-size: 11px; font-weight: 600; letter-spacing: 0.22em; text-transform: uppercase;
    color: rgb(var(--accent)); margin-bottom: 26px;
  }
  h1 { font-size: 20px; font-weight: 600; margin: 0 0 12px; letter-spacing: -0.01em; }
  p { margin: 0 auto 30px; max-width: 360px; font-size: 13px; line-height: 1.7; color: rgb(var(--muted)); }
  a.btn {
    display: inline-block; text-decoration: none; font-size: 12.5px; font-weight: 500;
    color: rgb(var(--text)); border: 1px solid rgb(var(--border));
    padding: 9px 18px; border-radius: 6px;
    transition: border-color .15s ease, color .15s ease;
  }
  a.btn:hover { border-color: rgb(var(--accent)); color: rgb(var(--accent)); }
  a.btn:focus-visible { outline: 2px solid rgb(245 158 11 / 0.8); outline-offset: 2px; }
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
