package auth

import (
	"html/template"
	"net/http"
)

// loginPage is dross's own sign-in screen. An unauthenticated browser lands
// here instead of being bounced straight to the identity provider, so the first
// thing a user sees is dross rather than a Cloudflare interstitial. The button
// starts the same OIDC flow the redirect used to.
var loginPage = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign in · Dross</title>
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; }
  body {
    margin: 0; min-height: 100dvh; display: grid; place-items: center;
    background:
      radial-gradient(900px 520px at 70% -10%, rgba(139,92,246,.20), transparent 60%),
      radial-gradient(700px 420px at 10% 10%, rgba(34,211,238,.10), transparent 55%),
      #07070F;
    color: #ECE9FB;
    font: 16px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", Inter, system-ui, sans-serif;
    -webkit-font-smoothing: antialiased;
  }
  .card {
    width: min(92vw, 380px); padding: 40px 32px 32px;
    background: #0E0E1A; border: 1px solid #241E3B; border-radius: 18px;
    text-align: center;
  }
  .mark { width: 96px; height: 96px; margin: 0 auto 18px; display: block; }
  h1 { margin: 0 0 6px; font-size: 26px; letter-spacing: -.02em; font-weight: 650; }
  p { margin: 0 0 28px; color: #7D7BA3; font-size: 14.5px; }
  .btn {
    display: flex; align-items: center; justify-content: center; gap: 10px;
    width: 100%; padding: 13px 16px; border-radius: 12px;
    background: #F4F1FC; color: #12101F; font-size: 15px; font-weight: 600;
    text-decoration: none; border: 0; cursor: pointer;
    transition: transform .06s ease, filter .15s ease;
  }
  .btn:hover { filter: brightness(.94); }
  .btn:active { transform: translateY(1px); }
  .btn:focus-visible { outline: 2px solid #A78BFA; outline-offset: 3px; }
  .hairline {
    height: 1px; margin: 26px 0 0;
    background: linear-gradient(90deg, transparent, #8B5CF6, #22D3EE, #E879F9, transparent);
    opacity: .55;
  }
  .foot { margin: 14px 0 0; font-size: 12.5px; color: #55547A; }
  @media (prefers-reduced-motion: reduce) { .btn { transition: none; } }
</style>
</head>
<body>
  <main class="card">
    <!-- The mascot, same geometry as the app's vector mark. -->
    <svg class="mark" viewBox="0 0 260 270" aria-hidden="true">
      <defs>
        <linearGradient id="b" x1="0.15" y1="0" x2="0.85" y2="1">
          <stop offset="0" stop-color="#B292F2"/><stop offset="0.5" stop-color="#8B5CF6"/>
          <stop offset="1" stop-color="#6320C9"/>
        </linearGradient>
        <radialGradient id="i" cx="0.38" cy="0.32" r="0.74">
          <stop offset="0" stop-color="#D6E6FF"/><stop offset="0.38" stop-color="#5D83CC"/>
          <stop offset="0.78" stop-color="#23397A"/><stop offset="1" stop-color="#151F4C"/>
        </radialGradient>
        <clipPath id="e"><circle cx="127" cy="104" r="51"/></clipPath>
      </defs>
      <g stroke="#140A24" stroke-linejoin="round" stroke-linecap="round">
        <g fill="none" stroke="#140A24">
          <path d="M70 148 Q52 158 40 173" stroke-width="50"/>
          <path d="M40 173 Q26 190 28 210" stroke-width="40"/>
          <path d="M28 210 Q32 231 53 232" stroke-width="30"/>
          <path d="M53 232 Q72 230 73 213" stroke-width="22"/>
          <path d="M190 148 Q208 158 220 173" stroke-width="50"/>
          <path d="M220 173 Q234 190 232 210" stroke-width="40"/>
          <path d="M232 210 Q228 231 207 232" stroke-width="30"/>
          <path d="M207 232 Q188 230 187 213" stroke-width="22"/>
        </g>
        <g fill="none" stroke="url(#b)">
          <path d="M70 148 Q52 158 40 173" stroke-width="42"/>
          <path d="M40 173 Q26 190 28 210" stroke-width="32"/>
          <path d="M28 210 Q32 231 53 232" stroke-width="22"/>
          <path d="M53 232 Q72 230 73 213" stroke-width="14"/>
          <path d="M190 148 Q208 158 220 173" stroke-width="42"/>
          <path d="M220 173 Q234 190 232 210" stroke-width="32"/>
          <path d="M232 210 Q228 231 207 232" stroke-width="22"/>
          <path d="M207 232 Q188 230 187 213" stroke-width="14"/>
        </g>
        <path d="M132 20 C184 22 218 66 214 116 C210 162 176 196 128 198
                 C80 200 40 168 40 118 C40 66 80 18 132 20 Z"
              fill="url(#b)" stroke-width="10"/>
        <circle cx="127" cy="104" r="51" fill="#F4F1FC" stroke-width="9"/>
        <circle cx="127" cy="108" r="32" fill="url(#i)"/>
        <circle cx="127" cy="108" r="10" fill="#0F1836"/>
        <ellipse cx="114" cy="96" rx="9" ry="12" fill="#fff" opacity=".9"/>
        <g clip-path="url(#e)">
          <path d="M74 46 L182 46 L182 96 Q127 78 74 108 Z" fill="url(#b)"/>
          <path d="M74 108 Q127 78 182 96" fill="none" stroke="#140A24" stroke-width="9"/>
        </g>
        <path d="M100 172 Q126 180 151 164" fill="none" stroke-width="9"/>
      </g>
    </svg>

    <h1>Dross</h1>
    <p>Sign in to continue.</p>

    <a class="btn" href="{{.LoginURL}}">
      <svg width="18" height="18" viewBox="0 0 48 48" aria-hidden="true">
        <path fill="#EA4335" d="M24 9.5c3.5 0 6.6 1.2 9.1 3.6l6.8-6.8C35.9 2.4 30.4 0 24 0 14.6 0 6.5 5.4 2.6 13.2l7.9 6.2C12.4 13.7 17.7 9.5 24 9.5z"/>
        <path fill="#4285F4" d="M46.1 24.6c0-1.6-.1-2.8-.4-4H24v7.5h12.7c-.3 2.1-1.6 5.2-4.7 7.3l7.3 5.6c4.3-4 6.8-9.8 6.8-16.4z"/>
        <path fill="#FBBC05" d="M10.5 28.6c-.5-1.5-.8-3-.8-4.6s.3-3.2.8-4.6l-7.9-6.2C1 16.3 0 20 0 24s1 7.7 2.6 10.8l7.9-6.2z"/>
        <path fill="#34A853" d="M24 48c6.5 0 11.9-2.1 15.9-5.8l-7.3-5.6c-2 1.4-4.6 2.3-8.6 2.3-6.3 0-11.6-4.2-13.5-9.9l-7.9 6.2C6.5 42.6 14.6 48 24 48z"/>
      </svg>
      Continue with Google
    </a>

    <div class="hairline"></div>
    <p class="foot">Access is limited to approved accounts.</p>
  </main>
</body>
</html>`))

// serveLoginPage renders the sign-in screen, carrying the originally requested
// path through so the user lands where they were headed.
func serveLoginPage(w http.ResponseWriter, loginURL string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// The page is self-contained; block anything it doesn't need.
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; img-src data:; form-action 'self'")
	_ = loginPage.Execute(w, struct{ LoginURL string }{loginURL})
}

// signedOutPage is shown after logout instead of the identity provider's own
// "you have been signed out" screen. The provider's logout URL is loaded in a
// hidden frame so its session still ends, but the user never leaves dross.
var signedOutPage = template.Must(template.New("signedout").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Signed out · Dross</title>
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; }
  body {
    margin: 0; min-height: 100dvh; display: grid; place-items: center;
    background:
      radial-gradient(900px 520px at 70% -10%, rgba(139,92,246,.20), transparent 60%),
      radial-gradient(700px 420px at 10% 10%, rgba(34,211,238,.10), transparent 55%),
      #07070F;
    color: #ECE9FB;
    font: 16px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", Inter, system-ui, sans-serif;
    -webkit-font-smoothing: antialiased;
  }
  .card {
    width: min(92vw, 380px); padding: 40px 32px 32px;
    background: #0E0E1A; border: 1px solid #241E3B; border-radius: 18px;
    text-align: center;
  }
  h1 { margin: 0 0 6px; font-size: 24px; letter-spacing: -.02em; font-weight: 650; }
  p { margin: 0 0 28px; color: #7D7BA3; font-size: 14.5px; }
  .btn {
    display: block; width: 100%; padding: 13px 16px; border-radius: 12px;
    background: #F4F1FC; color: #12101F; font-size: 15px; font-weight: 600;
    text-decoration: none;
  }
  .btn:focus-visible { outline: 2px solid #A78BFA; outline-offset: 3px; }
  .hairline {
    height: 1px; margin: 26px 0 0;
    background: linear-gradient(90deg, transparent, #8B5CF6, #22D3EE, #E879F9, transparent);
    opacity: .55;
  }
  iframe { display: none; }
</style>
</head>
<body>
  <main class="card">
    <h1>Signed out</h1>
    <p>Your session has ended.</p>
    <a class="btn" href="/auth/login?reauth=1">Sign in again</a>
    <div class="hairline"></div>
  </main>
  {{if .IdPLogoutURL}}<iframe src="{{.IdPLogoutURL}}" title="" aria-hidden="true" tabindex="-1"></iframe>{{end}}
</body>
</html>`))

// serveSignedOutPage renders dross's own post-logout screen.
func serveSignedOutPage(w http.ResponseWriter, idpLogout string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// Frames the provider's logout endpoint, so allow that one origin only.
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; frame-src https://*.cloudflareaccess.com; form-action 'self'")
	_ = signedOutPage.Execute(w, struct{ IdPLogoutURL string }{idpLogout})
}
