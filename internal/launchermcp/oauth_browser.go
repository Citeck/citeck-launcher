package launchermcp

import (
	"bytes"
	"context"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"github.com/citeck/citeck-launcher/internal/i18n"
)

// SetOAuthLocale sets the browser-page fallback, not a process-global CLI locale.
func (s *Service) SetOAuthLocale(locale string) {
	s.localeMu.Lock()
	defer s.localeMu.Unlock()
	s.oauthLocale = locale
}
func (s *Service) oauthTranslator(r *http.Request) *i18n.Translator {
	locale := r.Header.Get("X-Citeck-Locale")
	if locale == "" {
		s.localeMu.Lock()
		locale = s.oauthLocale
		s.localeMu.Unlock()
	}
	return i18n.NewTranslator(locale)
}
func oauthCookieName() string             { return "citeck_mcp_browser" }
func pendingBrowserPath(id string) string { return "/oauth/requests/" + id }

func (s *Service) authorizeOAuth(w http.ResponseWriter, r *http.Request) {
	if !oauthMethod(w, r, http.MethodGet) {
		return
	}
	if len(r.URL.RawQuery) > 8192 {
		oauthHTTPError(w, 400, "invalid_request", "authorization request too large")
		return
	}
	form, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		oauthHTTPError(w, 400, "invalid_request", "invalid query encoding")
		return
	}
	p, nonce, csrf, err := s.store.beginAuthorization(r.Context(), form)
	if err != nil {
		s.writeOAuthFailure(w, err)
		return
	}
	t := s.oauthTranslator(r)
	data := struct{ Title, Waiting, Unverified, Instruction, Correlation, Terminal, Name, Path, CSRF, Language, Code string }{
		Title: t.T("mcp.oauth.browser.title"), Waiting: t.T("mcp.oauth.browser.waiting"), Unverified: t.T("mcp.oauth.browser.unverified"), Instruction: t.T("mcp.oauth.browser.instruction"), Correlation: t.T("mcp.oauth.browser.correlation"), Terminal: t.T("mcp.oauth.browser.terminal"), Name: p.ClientName, Path: pendingBrowserPath(p.ID), CSRF: csrf, Language: t.Locale(), Code: p.CorrelationCode,
	}
	var page bytes.Buffer
	if err = oauthWaitingPage.Execute(&page, data); err != nil {
		oauthHTTPError(w, 500, "server_error", "page unavailable")
		return
	}
	//nolint:gosec // G124: the canonical OAuth issuer is HTTP loopback, so Secure would break the ceremony; HttpOnly and Strict are set.
	http.SetCookie(w, &http.Cookie{Name: oauthCookieName(), Value: nonce, Path: pendingBrowserPath(p.ID), HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 600})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+csrf+"'; style-src 'nonce-"+csrf+"'; connect-src 'self'; form-action http://127.0.0.1:* http://localhost:* http://[::1]:*; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	_, _ = w.Write(page.Bytes())
}

func (s *Service) browserOAuth(w http.ResponseWriter, r *http.Request, port int) {
	tail := strings.TrimPrefix(r.URL.Path, "/oauth/requests/")
	id, action, ok := strings.Cut(tail, "/")
	if !ok || len(id) != 64 || (action != "status" && action != "finish") {
		http.NotFound(w, r)
		return
	}
	method := http.MethodGet
	if action == "finish" {
		method = http.MethodPost
	}
	if !oauthMethod(w, r, method) {
		return
	}
	if r.URL.RawQuery != "" {
		oauthHTTPError(w, 400, "invalid_request", "query parameters refused")
		return
	}
	cookies := r.CookiesNamed(oauthCookieName())
	if len(cookies) != 1 {
		oauthHTTPError(w, 403, "invalid_request", "browser binding required")
		return
	}
	if action == "status" {
		status := ""
		err := s.store.oauthView(r.Context(), func(ctx context.Context) error {
			p, err := s.store.browserPending(ctx, id, cookies[0].Value)
			if err == nil {
				status = p.Status
			}
			return err
		})
		if err != nil {
			oauthHTTPError(w, 404, "invalid_request", "request unavailable")
			return
		}
		oauthJSON(w, 200, map[string]string{"status": status})
		return
	}
	if origins := r.Header.Values("Origin"); len(origins) != 1 || origins[0] != canonicalIssuer(port) {
		oauthHTTPError(w, 403, "invalid_request", "same-origin completion required")
		return
	}
	form, err := oauthForm(w, r)
	if err != nil || len(form) != 1 || len(form["csrf"]) != 1 {
		oauthHTTPError(w, 400, "invalid_request", "CSRF form required")
		return
	}
	callback, err := s.store.finishAuthorization(r.Context(), id, cookies[0].Value, form.Get("csrf"))
	if err != nil {
		s.writeOAuthFailure(w, err)
		return
	}
	// Ordinary browser navigation follows this redirect, not a fetch whose
	// cross-origin response would be blocked before reaching the native callback.
	http.Redirect(w, r, callback, http.StatusSeeOther)
}

var oauthWaitingPage = template.Must(template.New("oauth-waiting").Parse(`<!doctype html>
<html lang="{{.Language}}"><head><meta charset="utf-8"><meta name="referrer" content="same-origin"><meta name="viewport" content="width=device-width"><title>{{.Title}}</title>
<style nonce="{{.CSRF}}">body{font:16px system-ui,sans-serif;max-width:40rem;margin:4rem auto;padding:1rem;background:#202124;color:#eee}code{font-size:1.4em}p{line-height:1.6}</style></head>
<body><h1>{{.Title}}</h1><p id="status">{{.Waiting}}</p><p>{{.Unverified}}: <b>{{.Name}}</b></p><p>{{.Instruction}}</p><p>{{.Correlation}}: <code>{{.Code}}</code></p>
<form method="post" action="{{.Path}}/finish"><input type="hidden" name="csrf" value="{{.CSRF}}"></form><p id="terminal" hidden>{{.Terminal}}</p>
<script nonce="{{.CSRF}}">const form=document.querySelector('form');const until=Date.now()+305000;async function poll(){try{const response=await fetch(form.action.replace(/\/finish$/, '/status'),{credentials:'same-origin',cache:'no-store'});if(!response.ok)throw new Error();const data=await response.json();if(['approved','denied','expired','cancelled'].includes(data.status)){form.submit();return;}if(data.status!=='pending'||Date.now()>until)throw new Error();setTimeout(poll,1500);}catch{document.getElementById('status').hidden=true;document.getElementById('terminal').hidden=false;}}setTimeout(poll,1500);</script></body></html>`))
