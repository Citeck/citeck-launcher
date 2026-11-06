package launchermcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/ory/fosite"
)

type oauthRateWindow struct {
	at    time.Time
	count int
}

// Fixed route buckets bound anonymous local work, not a per-IP identity system.
func (s *Service) allowOAuthRequest(bucket string, limit int) bool {
	s.oauthRateMu.Lock()
	defer s.oauthRateMu.Unlock()
	if s.oauthRates == nil {
		s.oauthRates = map[string]oauthRateWindow{}
	}
	window := s.oauthRates[bucket]
	now := time.Now()
	if now.Sub(window.at) >= time.Minute {
		window = oauthRateWindow{at: now}
	}
	if window.count >= limit {
		return false
	}
	window.count++
	s.oauthRates[bucket] = window
	return true
}
func oauthJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func oauthHTTPError(w http.ResponseWriter, status int, code, description string) {
	oauthJSON(w, status, map[string]string{"error": code, "error_description": description})
}
func oauthMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		w.Header().Set("Allow", method)
		oauthHTTPError(w, 405, "invalid_request", "method not allowed")
		return false
	}
	return true
}
func oauthBody(w http.ResponseWriter, r *http.Request, media string) ([]byte, error) {
	if len(r.Header.Values("Content-Type")) != 1 {
		return nil, errors.New("one content type required")
	}
	kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || kind != media {
		return nil, errors.New("unsupported content type")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, errors.New("invalid or oversized body")
	}
	return b, nil
}
func oauthForm(w http.ResponseWriter, r *http.Request) (url.Values, error) {
	if r.URL.RawQuery != "" || len(r.Header.Values("Authorization")) > 0 {
		return nil, errors.New("body parameters and public client_id required")
	}
	b, err := oauthBody(w, r, "application/x-www-form-urlencoded")
	if err != nil {
		return nil, err
	}
	form, err := url.ParseQuery(string(b))
	if err != nil {
		return nil, errors.New("invalid form encoding")
	}
	return form, nil
}

func (s *Service) oauthMetadata(w http.ResponseWriter, r *http.Request, port int) {
	if !oauthMethod(w, r, http.MethodGet) {
		return
	}
	if r.URL.RawQuery != "" {
		oauthHTTPError(w, 400, "invalid_request", "metadata takes no parameters")
		return
	}
	issuer := canonicalIssuer(port)
	resource := canonicalResource(port)
	if r.URL.Path != "/.well-known/oauth-authorization-server" {
		oauthJSON(w, 200, map[string]any{"resource": resource, "authorization_servers": []string{issuer}, "scopes_supported": []string{oauthScope}, "bearer_methods_supported": []string{"header"}})
		return
	}
	oauthJSON(w, 200, map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "registration_endpoint": issuer + "/register", "revocation_endpoint": issuer + "/revoke", "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "token_endpoint_auth_methods_supported": []string{"none"}, "revocation_endpoint_auth_methods_supported": []string{"none"}, "code_challenge_methods_supported": []string{"S256"}, "scopes_supported": []string{oauthScope}, "authorization_response_iss_parameter_supported": true})
}

// Returns false only for a path outside the public OAuth allowlist. It never
// forwards to the daemon/UI mux and never adds CORS headers.
func (s *Service) serveOAuth(w http.ResponseWriter, r *http.Request, port int, host string) bool {
	metadata := slices.Contains([]string{"/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource", "/.well-known/oauth-authorization-server"}, r.URL.Path)
	if !metadata && !slices.Contains([]string{"/register", "/authorize", "/token", "/revoke"}, r.URL.Path) && !strings.HasPrefix(r.URL.Path, "/oauth/requests/") {
		return false
	}
	settings := s.store.Settings()
	if !settings.Enabled || settings.Port != port {
		oauthHTTPError(w, 503, "temporarily_unavailable", "MCP listener configuration changed")
		return true
	}
	w.Header().Set("Pragma", "no-cache")
	// Keep the Origin on the same-origin completion POST, but never send
	// a Referer to the native client callback on another origin.
	w.Header().Set("Referrer-Policy", "same-origin")
	if metadata {
		s.oauthMetadata(w, r, port)
		return true
	}
	if host != "127.0.0.1" {
		oauthHTTPError(w, 400, "invalid_request", "OAuth requires the canonical MCP URL: "+canonicalResource(port))
		return true
	}
	origins := r.Header.Values("Origin")
	browser := r.URL.Path == "/authorize" || strings.HasPrefix(r.URL.Path, "/oauth/requests/")
	if len(origins) > 0 && (!browser || len(origins) != 1 || origins[0] != canonicalIssuer(port)) {
		oauthHTTPError(w, 403, "invalid_request", "origin refused")
		return true
	}
	bucket, limit := r.URL.Path, 120
	switch {
	case r.URL.Path == "/register":
		limit = 12
	case r.URL.Path == "/authorize":
		limit = 20
	case strings.HasPrefix(r.URL.Path, "/oauth/requests/"):
		bucket = "browser"
		limit = 240
	}
	if !s.allowOAuthRequest(bucket, limit) {
		w.Header().Set("Retry-After", "60")
		oauthHTTPError(w, 429, "temporarily_unavailable", "too many requests")
		return true
	}
	switch r.URL.Path {
	case "/register":
		s.registerOAuthClient(w, r)
	case "/authorize":
		s.authorizeOAuth(w, r)
	case "/token":
		s.exchangeOAuth(w, r)
	case "/revoke":
		s.revokeOAuth(w, r)
	default:
		s.browserOAuth(w, r, port)
	}
	return true
}

// DCR accepts bounded standard optional metadata as inert JSON. Recognized
// authority fields are checked explicitly; nothing is fetched or rendered HTML.
func decodeOAuthRegistration(b []byte) (clientName string, clientRedirects []string, resultErr error) {
	fields, err := decodeOAuthMetadata(b)
	if err != nil {
		return "", nil, err
	}
	var name string
	var redirects []string
	if raw, ok := fields["client_name"]; ok {
		if len(raw) == 0 || raw[0] != '"' {
			return "", nil, errors.New("invalid client name")
		}
		if err = json.Unmarshal(raw, &name); err != nil {
			return "", nil, errors.New("invalid client name")
		}
	}
	if len(name) > 100 || strings.ContainsFunc(name, unicode.IsControl) {
		return "", nil, errors.New("invalid client name")
	}
	if err = json.Unmarshal(fields["redirect_uris"], &redirects); err != nil || len(redirects) < 1 || len(redirects) > 8 {
		return "", nil, errors.New("registered loopback redirects required")
	}
	for i, redirect := range redirects {
		if len(redirect) > 2048 || !validOAuthRedirect(redirect) || slices.Contains(redirects[:i], redirect) {
			return "", nil, errors.New("invalid registered loopback redirect")
		}
	}
	for key, expected := range map[string]string{"token_endpoint_auth_method": "none", "application_type": "native", "scope": oauthScope} {
		if raw, ok := fields[key]; ok {
			var value string
			if err = json.Unmarshal(raw, &value); err != nil || value != expected {
				return "", nil, errors.New("unsupported " + key)
			}
		}
	}
	for key, allowed := range map[string][]string{"grant_types": {"authorization_code", "refresh_token"}, "response_types": {"code"}} {
		if raw, ok := fields[key]; ok {
			var values []string
			if err = json.Unmarshal(raw, &values); err != nil || len(values) == 0 || len(values) > len(allowed) {
				return "", nil, errors.New("unsupported " + key)
			}
			for i, v := range values {
				if !slices.Contains(allowed, v) || slices.Contains(values[:i], v) {
					return "", nil, errors.New("unsupported " + key)
				}
			}
		}
	}
	for _, key := range []string{"role", "requested_role", "roles", "audience", "resource"} {
		if _, ok := fields[key]; ok {
			return "", nil, errors.New("client metadata cannot select authority")
		}
	}
	return name, redirects, nil
}
func (s *Service) registerOAuthClient(w http.ResponseWriter, r *http.Request) {
	if !oauthMethod(w, r, http.MethodPost) {
		return
	}
	if r.URL.RawQuery != "" {
		oauthHTTPError(w, 400, "invalid_client_metadata", "query parameters are not accepted")
		return
	}
	b, err := oauthBody(w, r, "application/json")
	if err != nil {
		oauthHTTPError(w, 400, "invalid_client_metadata", err.Error())
		return
	}
	name, redirects, err := decodeOAuthRegistration(b)
	if err != nil {
		oauthHTTPError(w, 400, "invalid_client_metadata", err.Error())
		return
	}
	c, err := s.store.oauth.registerClient(r.Context(), name, redirects)
	if err != nil {
		oauthHTTPError(w, 503, "temporarily_unavailable", "registration unavailable; retry later")
		return
	}
	oauthJSON(w, 201, map[string]any{"client_id": c.ID, "client_id_issued_at": c.CreatedAt.Unix(), "client_name": c.Name, "redirect_uris": c.Redirects, "token_endpoint_auth_method": "none", "application_type": "native", "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}, "scope": oauthScope})
}
func (s *Service) exchangeOAuth(w http.ResponseWriter, r *http.Request) {
	if !oauthMethod(w, r, http.MethodPost) {
		return
	}
	form, err := oauthForm(w, r)
	if err != nil {
		oauthHTTPError(w, 400, "invalid_request", err.Error())
		return
	}
	if err = adaptOAuth(cloneOAuthForm(form), canonicalResource(s.store.Settings().Port), form.Get("grant_type") == "refresh_token", false); err != nil {
		oauthHTTPError(w, 400, "invalid_request", err.Error())
		return
	}
	out, err := s.store.oauth.exchange(r.Context(), form)
	if err != nil {
		s.writeOAuthFailure(w, err)
		return
	}
	s.store.oauth.provider.WriteAccessResponse(r.Context(), w, nil, out)
}
func (s *Service) writeOAuthFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrOAuthRequestUnavailable) {
		oauthHTTPError(w, 409, "invalid_request", "request unavailable or already completed")
		return
	}
	if errors.Is(err, fosite.ErrServerError) {
		oauthHTTPError(w, 500, "server_error", "OAuth operation failed; retry later")
		return
	}
	if errors.Is(err, fosite.ErrTemporarilyUnavailable) {
		oauthHTTPError(w, 503, "temporarily_unavailable", "OAuth operation unavailable; retry later")
		return
	}
	var protocol *fosite.RFC6749Error
	if errors.As(err, &protocol) {
		code := "invalid_grant"
		if errors.Is(err, fosite.ErrInvalidRequest) {
			code = "invalid_request"
		}
		if errors.Is(err, fosite.ErrInvalidClient) {
			code = "invalid_client"
		}
		if errors.Is(err, fosite.ErrUnsupportedGrantType) {
			code = "unsupported_grant_type"
		}
		oauthHTTPError(w, 400, code, "OAuth request refused")
		return
	}
	oauthHTTPError(w, 503, "temporarily_unavailable", "OAuth operation was not confirmed; retry or start a new sign in")
}
func (s *Service) revokeOAuth(w http.ResponseWriter, r *http.Request) {
	if !oauthMethod(w, r, http.MethodPost) {
		return
	}
	form, err := oauthForm(w, r)
	if err != nil {
		oauthHTTPError(w, 400, "invalid_request", err.Error())
		return
	}
	if form.Get("client_id") == "" || form.Get("token") == "" {
		oauthHTTPError(w, 400, "invalid_request", "client_id and token required")
		return
	}
	if err = adaptOAuthRevocation(form); err != nil {
		oauthHTTPError(w, 400, "invalid_request", "invalid revocation parameters")
		return
	}
	err = s.store.oauth.revokeToken(r.Context(), form)
	s.store.oauth.writeRevocation(w, err)
}

func decodeOAuthMetadata(b []byte) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return nil, errors.New("JSON object required")
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		key, e := d.Token()
		if e != nil {
			return nil, errors.New("invalid metadata")
		}
		k, ok := key.(string)
		if !ok {
			return nil, errors.New("invalid metadata key")
		}
		if _, ok = fields[k]; ok {
			return nil, errors.New("duplicate metadata field")
		}
		var value json.RawMessage
		if e = d.Decode(&value); e != nil {
			return nil, errors.New("invalid metadata value")
		}
		fields[k] = value
	}
	if _, err = d.Token(); err != nil {
		return nil, errors.New("invalid metadata")
	}
	if err = d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing metadata")
	}

	return fields, nil
}
