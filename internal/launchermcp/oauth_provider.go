package launchermcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/ory/fosite"
	"github.com/ory/fosite/compose"
	core "github.com/ory/fosite/handler/oauth2"
)

// Private core, not an owner-proof or HTTP surface. Browser authorization must
// first use the pending/native-approval boundary before calling mintApprovedCode.
type oauthProvider struct {
	store    *Store
	storage  *oauthStorage
	provider fosite.OAuth2Provider
	strategy core.CoreStrategy
}

func oauthConfig(key []byte) *fosite.Config {
	return &fosite.Config{GlobalSecret: slices.Clone(key), AccessTokenLifespan: 10 * time.Minute, RefreshTokenLifespan: oauthGrantLifetime, AuthorizeCodeLifespan: time.Minute, EnforcePKCE: true, EnforcePKCEForPublicClients: true, RefreshTokenScopes: []string{}, ScopeStrategy: fosite.ExactScopeStrategy, AudienceMatchingStrategy: fosite.ExactAudienceMatchingStrategy, DisableRefreshTokenValidation: true}
}
func newOAuthProvider(s *Store) *oauthProvider {
	cfg := oauthConfig(s.key)
	strategy := compose.NewOAuth2HMACStrategy(cfg)
	storage := &oauthStorage{store: s}
	provider := compose.Compose(cfg, storage, strategy, compose.OAuth2AuthorizeExplicitFactory, compose.OAuth2RefreshTokenGrantFactory, compose.OAuth2PKCEFactory, compose.OAuth2TokenRevocationFactory, compose.OAuth2TokenIntrospectionFactory)
	return &oauthProvider{store: s, storage: storage, provider: provider, strategy: strategy}
}

func (p *oauthProvider) registerClient(ctx context.Context, name string, redirects []string) (oauthClient, error) {
	if len(name) > 100 || len(redirects) < 1 || len(redirects) > 8 {
		return oauthClient{}, errors.New("invalid OAuth client metadata")
	}
	for _, redirect := range redirects {
		if len(redirect) > 2048 || !validOAuthRedirect(redirect) {
			return oauthClient{}, errors.New("invalid registered loopback redirect")
		}
	}
	id, err := randomOAuthID()
	if err != nil {
		return oauthClient{}, err
	}
	now := time.Now().UTC()
	client := oauthClient{ID: id, Name: name, Redirects: slices.Clone(redirects), CreatedAt: now, ExpiresAt: now.Add(oauthGrantLifetime)}
	err = p.store.oauthWrite(ctx, func(ctx context.Context) error {
		if !p.store.transaction(ctx).settings.Enabled {
			return errors.New("MCP is disabled")
		}
		if collectErr := p.store.collectOAuth(ctx); collectErr != nil {
			return collectErr
		}
		redirectsJSON, encodeErr := json.Marshal(client.Redirects)
		if encodeErr != nil {
			return mcpStorageError(encodeErr)
		}
		_, insertErr := p.storage.q(ctx).ExecContext(ctx, "INSERT INTO oauth_clients(id,name,expiry,redirects,created,epoch) VALUES(?,?,?,?,?,?)", client.ID, client.Name, client.ExpiresAt.UnixNano(), string(redirectsJSON), client.CreatedAt.UnixNano(), p.store.transaction(ctx).settings.Epoch)
		return mcpStorageError(insertErr)
	})
	if err != nil {
		return oauthClient{}, err
	}
	return client, nil
}

// This adapter translates only a validated RFC8707 resource to Fosite audience.
// A missing refresh resource inherits the original; an explicit empty does not.
func adaptOAuth(form url.Values, resource string, refresh, authorize bool) error {
	allowed := []string{"client_id", "resource", "scope"}
	if authorize {
		allowed = append(allowed, "redirect_uri", "response_type", "state", "code_challenge", "code_challenge_method")
	} else {
		allowed = append(allowed, "grant_type", "redirect_uri", "code", "code_verifier", "refresh_token")
	}
	for key, values := range form {
		if !slices.Contains(allowed, key) {
			if slices.Contains([]string{"role", "requested_role", "roles", "audience", "client_secret", "client_assertion", "client_assertion_type"}, key) {
				return fmt.Errorf("unsupported authority parameter %s", key)
			}
			form.Del(key)
			continue
		}
		if len(values) != 1 {
			return fmt.Errorf("invalid OAuth parameter %s", key)
		}
	}
	if _, present := form["resource"]; !present {
		if !refresh {
			return errors.New("OAuth resource required")
		}
	} else if form.Get("resource") != resource {
		return errors.New("invalid OAuth resource")
	}
	if values, present := form["scope"]; present && values[0] != oauthScope {
		return errors.New("invalid OAuth scope")
	}
	form.Del("resource")
	if !refresh {
		form.Set("audience", resource)
	}
	if authorize && form.Get("scope") == "" {
		form.Set("scope", oauthScope)
	}
	return nil
}
func oauthHTTPRequest(endpoint string, form url.Values) *http.Request {
	request, _ := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return request
}
func cloneOAuthForm(form url.Values) url.Values {
	next := url.Values{}
	for key, values := range form {
		next[key] = slices.Clone(values)
	}
	return next
}
func (p *oauthProvider) validateRedirect(ctx context.Context, form url.Values) error {
	client, err := p.storage.GetClient(ctx, form.Get("client_id"))
	if err != nil {
		return mcpStorageError(err)
	}
	if !matchesOAuthRedirect(client.GetRedirectURIs(), form.Get("redirect_uri")) {
		return fosite.ErrInvalidRequest.WithDescription("registered OAuth redirect required")
	}
	return nil
}

func (p *oauthProvider) mintApprovedCode(ctx context.Context, input url.Values, role Role, approvedAt time.Time) (string, error) {
	// Test helper for the provider core. Public routes go through the pending
	// ceremony and trusted owner decision, never these approval arguments.
	if !role.Valid() || approvedAt.After(time.Now()) || !time.Now().Before(approvedAt.Add(oauthGrantLifetime)) {
		return "", errors.New("invalid owner approval")
	}
	var code string
	err := p.store.oauthWrite(ctx, func(ctx context.Context) error {
		var err error
		code, err = p.mintApprovedCodeInTransaction(ctx, input, role, approvedAt)
		return err
	})
	if err != nil {
		return "", mcpStorageError(err)
	}
	return code, nil
}

// Caller owns the outer SQL UOW, including consuming the browser completion.
func (p *oauthProvider) mintApprovedCodeInTransaction(ctx context.Context, input url.Values, role Role, approvedAt time.Time, options ...OAuthApproval) (string, error) {
	deadline := approvedAt.Add(oauthGrantLifetime)
	name := ""
	if len(options) == 1 {
		name = options[0].Name
		if options[0].ExpiresAt != nil {
			deadline = *options[0].ExpiresAt
		}
	}
	var code string
	form := cloneOAuthForm(input)
	err := func() error {
		settings := p.store.transaction(ctx).settings.Settings
		if !settings.Enabled {
			return errors.New("MCP is disabled")
		}
		resource := canonicalResource(settings.Port)
		if err := adaptOAuth(form, resource, false, true); err != nil {
			return mcpStorageError(err)
		}
		if err := p.validateRedirect(ctx, form); err != nil {
			return mcpStorageError(err)
		}
		clientID := form.Get("client_id")
		if err := p.store.collectOAuth(ctx); err != nil {
			return mcpStorageError(err)
		}
		request, _ := http.NewRequest(http.MethodGet, canonicalIssuer(settings.Port)+"/authorize?"+form.Encode(), http.NoBody)
		ar, err := p.provider.NewAuthorizeRequest(ctx, request)
		if err != nil {
			return fmt.Errorf("OAuth authorization request: %w", err)
		}
		ar.GrantScope(oauthScope)
		ar.GrantAudience(resource)
		id := ar.GetID()
		_, err = p.storage.q(ctx).ExecContext(ctx, "INSERT INTO oauth_grants(id,client,resource,role,approved,deadline,epoch,name) VALUES(?,?,?,?,?,?,?,?)", id, clientID, resource, role, approvedAt.UnixNano(), deadline.UnixNano(), p.store.transaction(ctx).settings.Epoch, name)
		if err != nil {
			return mcpStorageError(err)
		}
		out, err := p.provider.NewAuthorizeResponse(ctx, ar, &oauthSession{GrantID: id, Deadline: deadline})
		if err != nil {
			return fmt.Errorf("OAuth authorization response: %w", err)
		}
		code = out.GetCode()
		return nil
	}()
	return code, err
}

func (p *oauthProvider) exchange(ctx context.Context, input url.Values) (fosite.AccessResponder, error) {
	form := cloneOAuthForm(input)
	var response fosite.AccessResponder
	err := p.store.oauthWrite(ctx, func(ctx context.Context) error {
		refresh := form.Get("grant_type") == "refresh_token"
		if !refresh && form.Get("grant_type") != "authorization_code" {
			return fosite.ErrUnsupportedGrantType
		}
		resource := canonicalResource(p.store.transaction(ctx).settings.Port)
		if err := adaptOAuth(form, resource, refresh, false); err != nil {
			return mcpStorageError(err)
		}
		if !refresh {
			if err := p.validateRedirect(ctx, form); err != nil {
				return mcpStorageError(err)
			}
		}
		if err := p.store.collectOAuth(ctx); err != nil {
			return mcpStorageError(err)
		}
		if err := p.prevalidateExchange(ctx, form, refresh); err != nil {
			return mcpStorageError(err)
		}
		ar, err := p.provider.NewAccessRequest(ctx, oauthHTTPRequest(canonicalIssuer(p.store.transaction(ctx).settings.Port)+"/token", form), &oauthSession{})
		if err != nil {
			return fmt.Errorf("OAuth access request: %w", err)
		}
		response, err = p.provider.NewAccessResponse(ctx, ar)
		if err != nil {
			return fmt.Errorf("OAuth access response: %w", err)
		}
		_, err = p.storage.q(ctx).ExecContext(ctx, "UPDATE oauth_clients SET expiry=max(expiry,?) WHERE id=?", time.Now().Add(oauthGrantLifetime).UnixNano(), form.Get("client_id"))
		return mcpStorageError(err)
	})
	// An in-memory response is not a credential until the outer commit succeeds.
	if err != nil {
		return nil, mcpStorageError(err)
	}
	return response, nil
}
func (p *oauthProvider) prevalidateExchange(ctx context.Context, form url.Values, refresh bool) error {
	// Fosite tests replay before binding/HMAC. Authenticate even tombstones
	// BEFORE allowing the library to revoke the family they refer to.
	var r fosite.Requester
	var err error
	if refresh {
		r, err = p.storage.GetRefreshTokenSession(ctx, p.strategy.RefreshTokenSignature(ctx, form.Get("refresh_token")), nil)
	} else {
		r, err = p.storage.GetAuthorizeCodeSession(ctx, p.strategy.AuthorizeCodeSignature(ctx, form.Get("code")), nil)
	}
	if err != nil && !errors.Is(err, fosite.ErrInactiveToken) && !errors.Is(err, fosite.ErrInvalidatedAuthorizeCode) {
		return mcpStorageError(err)
	}
	if r == nil {
		return mcpStorageError(err)
	}
	if r.GetClient().GetID() != form.Get("client_id") {
		return fosite.ErrInvalidClient
	}
	if err := p.storage.grantAvailable(ctx, r.GetID()); err != nil {
		return mcpStorageError(err)
	}
	if refresh {
		if err := p.strategy.ValidateRefreshToken(ctx, r, form.Get("refresh_token")); err != nil {
			return fmt.Errorf("OAuth refresh: %w", err)
		}
		return nil
	}
	if r.GetRequestForm().Get("redirect_uri") != form.Get("redirect_uri") {
		return fosite.ErrInvalidGrant.WithDescription("OAuth code redirect mismatch")
	}
	if err := p.strategy.ValidateAuthorizeCode(ctx, r, form.Get("code")); err != nil {
		return fmt.Errorf("OAuth code: %w", err)
	}
	return nil
}

func (p *oauthProvider) inspect(ctx context.Context, value string) (fosite.AccessRequester, error) {
	var result fosite.AccessRequester
	err := p.store.oauthView(ctx, func(ctx context.Context) error {
		kind, r, err := p.provider.IntrospectToken(ctx, value, fosite.AccessToken, &oauthSession{}, oauthScope)
		if err != nil {
			return fmt.Errorf("OAuth access: %w", err)
		}
		if kind != fosite.AccessToken || !r.GetGrantedAudience().ExactOne(canonicalResource(p.store.transaction(ctx).settings.Port)) {
			return fosite.ErrInactiveToken
		}
		result = r
		return nil
	})
	return result, err
}

// Strip inert extension fields before Fosite, but validate every recognized
// revocation/authority field (including duplicate client/token parameters).
func adaptOAuthRevocation(form url.Values) error {
	for key, values := range form {
		if !slices.Contains([]string{"client_id", "token", "token_type_hint"}, key) {
			if slices.Contains([]string{"client_secret", "client_assertion", "client_assertion_type", "role", "resource", "audience"}, key) {
				return fosite.ErrInvalidRequest
			}
			form.Del(key)
			continue
		}
		if len(values) != 1 {
			return fosite.ErrInvalidRequest
		}
	}
	return nil
}
func (p *oauthProvider) revokeToken(ctx context.Context, input url.Values) error {
	form := cloneOAuthForm(input)
	if err := adaptOAuthRevocation(form); err != nil {
		return err
	}
	var id string
	// Authenticate before begin: a known revoke whose transaction cannot start
	// still denies its family in RAM, rather than returning RFC7009 false success.
	err := p.store.oauthView(ctx, func(ctx context.Context) error {
		r, e := p.authenticatedRevocation(ctx, form.Get("token"))
		if e != nil {
			return mcpStorageError(e)
		}
		if r != nil {
			if r.GetClient().GetID() != form.Get("client_id") {
				return fosite.ErrInvalidClient
			}
			id = r.GetID()
		}
		return nil
	})
	if err != nil {
		return mcpStorageError(err)
	}
	if id == "" {
		return nil
	}
	return p.store.writeRevocation(ctx, id, func(ctx context.Context) error {
		r, e := p.authenticatedRevocation(ctx, form.Get("token"))
		if e != nil {
			return mcpStorageError(e)
		}
		if r == nil || r.GetID() != id || r.GetClient().GetID() != form.Get("client_id") {
			return fosite.ErrInvalidClient
		}
		if e = p.provider.NewRevocationRequest(ctx, oauthHTTPRequest(canonicalIssuer(p.store.transaction(ctx).settings.Port)+"/revoke", form)); e != nil {
			return mcpStorageError(e)
		}
		return p.storage.revoke(ctx, id)
	})
}
func (p *oauthProvider) authenticatedRevocation(ctx context.Context, value string) (fosite.Requester, error) {
	r, err := p.storage.refresh(ctx, p.strategy.RefreshTokenSignature(ctx, value), nil, false)
	if err != nil && !errors.Is(err, fosite.ErrNotFound) && (r == nil || !errors.Is(err, fosite.ErrInactiveToken)) {
		return nil, err
	}
	if r != nil {
		if e := p.strategy.ValidateRefreshToken(ctx, r, value); e != nil {
			return nil, mcpStorageError(e)
		}
		return r, nil
	}
	r, err = p.storage.access(ctx, p.strategy.AccessTokenSignature(ctx, value), nil, false)
	if err != nil && !errors.Is(err, fosite.ErrNotFound) && (r == nil || !errors.Is(err, fosite.ErrInactiveToken)) {
		return nil, err
	}
	if r != nil {
		if e := p.strategy.ValidateAccessToken(ctx, r, value); e != nil {
			return nil, mcpStorageError(e)
		}
		return r, nil
	}
	return nil, nil
}

// Fosite's generic-error RFC7009 response is deliberately 200. Do not invoke
// that writer for an unsuccessful outer transaction (especially commit failure).
func (p *oauthProvider) writeRevocation(w http.ResponseWriter, err error) {
	if err != nil {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		http.Error(w, "revocation not confirmed; retry", http.StatusServiceUnavailable)
		return
	}
	p.provider.WriteRevocationResponse(context.Background(), w, nil)
}
