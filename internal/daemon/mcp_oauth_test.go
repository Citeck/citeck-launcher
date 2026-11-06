package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/launchermcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// One connected client flow through real public HTTP routes and the real daemon
// management handlers reached over an isolated Unix socket. No store approval
// shortcut and no running Launcher, profile, Docker or external provider.
func TestMCPOAuthClientFlow(t *testing.T) {
	for _, role := range []launchermcp.Role{launchermcp.Observer, launchermcp.Developer} {
		t.Run(string(role), func(t *testing.T) { testMCPOAuthClientFlow(t, role) })
	}
}
func testMCPOAuthClientFlow(t *testing.T, role launchermcp.Role) {
	d := mcpFixture(t)
	store, err := launchermcp.OpenStore(filepath.Join(t.TempDir(), "mcp.db"))
	require.NoError(t, err)
	d.mcpStore = store
	d.mcp = launchermcp.New(store, mcpBackend{d})
	defer d.mcp.Close()
	public := httptest.NewUnstartedServer(nil)
	_, p, err := net.SplitHostPort(public.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(p)
	require.NoError(t, err)
	require.NoError(t, store.SetSettings(launchermcp.Settings{Enabled: true, Port: port}))
	public.Config.Handler = d.mcp.Handler(port)
	public.Start()
	defer public.Close()
	endpoint := public.URL + "/mcp"
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	socket := filepath.Join(t.TempDir(), "s")
	listener, err := net.Listen("unix", socket)
	require.NoError(t, err)
	ownerServer := &http.Server{Handler: unixHandlerChain(mux), ReadHeaderTimeout: time.Second}
	defer ownerServer.Close()
	go func() { _ = ownerServer.Serve(listener) }()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	owner := &http.Client{Transport: transport}
	httpClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(client *http.Client, method, path, media, body string) *http.Response {
		t.Helper()
		r, e := http.NewRequest(method, path, strings.NewReader(body))
		require.NoError(t, e)
		if media != "" {
			r.Header.Set("Content-Type", media)
		}
		resp, e := client.Do(r)
		require.NoError(t, e)
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}
	decode := func(resp *http.Response, out any) {
		t.Helper()
		require.NoError(t, json.NewDecoder(resp.Body).Decode(out))
		require.NoError(t, resp.Body.Close())
	}
	unauth := request(httpClient, "POST", endpoint, "application/json", `{}`)
	require.Equal(t, 401, unauth.StatusCode)
	require.Contains(t, unauth.Header.Get("WWW-Authenticate"), public.URL+"/.well-known/oauth-protected-resource/mcp")
	var resource struct {
		Resource string   `json:"resource"`
		Servers  []string `json:"authorization_servers"`
	}
	decode(request(httpClient, "GET", public.URL+"/.well-known/oauth-protected-resource/mcp", "", ""), &resource)
	require.Equal(t, endpoint, resource.Resource)
	require.Equal(t, []string{public.URL}, resource.Servers)
	var metadata map[string]any
	decode(request(httpClient, "GET", resource.Servers[0]+"/.well-known/oauth-authorization-server", "", ""), &metadata)
	require.Equal(t, public.URL, metadata["issuer"])
	var registration struct {
		ID string `json:"client_id"`
	}
	registered := request(httpClient, "POST", metadata["registration_endpoint"].(string), "application/json", `{"client_name":"Pi <unverified>","application_type":"native","token_endpoint_auth_method":"none","redirect_uris":["http://127.0.0.1:32123/callback"],"software_id":"test-sdk"}`)
	require.Equal(t, 201, registered.StatusCode)
	decode(registered, &registration)
	verifier := strings.Repeat("v", 48)
	sum := sha256.Sum256([]byte(verifier))
	callback := "http://127.0.0.1:42124/callback"
	auth := url.Values{"client_id": {registration.ID}, "redirect_uri": {callback}, "response_type": {"code"}, "state": {"state-0000000000000000000000000000"}, "resource": {endpoint}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}, "sdk_extension": {"ignored"}}
	page := request(httpClient, "GET", metadata["authorization_endpoint"].(string)+"?"+auth.Encode(), "", "")
	require.Equal(t, 200, page.StatusCode)
	html, err := io.ReadAll(page.Body)
	require.NoError(t, err)
	require.Contains(t, string(html), "Pi &lt;unverified&gt;")
	require.NotContains(t, string(html), "Pi <unverified>")
	cookies := page.Cookies()
	require.Len(t, cookies, 1)
	csrf := regexp.MustCompile(`name="csrf" value="([a-f0-9]{64})"`).FindStringSubmatch(string(html))
	require.Len(t, csrf, 2)
	var pending struct {
		Pending []launchermcp.OAuthPending `json:"pending"`
	}
	decode(request(owner, "GET", "http://owner/api/v1/mcp/oauth/pending", "", ""), &pending)
	require.Len(t, pending.Pending, 1)
	id := pending.Pending[0].ID
	require.Contains(t, string(html), pending.Pending[0].CorrelationCode)
	// Headers, localhost and a browser cookie cannot manufacture owner provenance.
	for _, suffix := range []string{"/pending", "/pending/" + id + "/approve", "/pending/" + id + "/deny", "/connections", "/connections/unknown"} {
		method := "GET"
		if strings.HasSuffix(suffix, "approve") || strings.HasSuffix(suffix, "deny") {
			method = "POST"
		}
		if strings.HasSuffix(suffix, "unknown") {
			method = "DELETE"
		}
		r := httptest.NewRequest(method, "/api/v1/mcp/oauth"+suffix, strings.NewReader(`{"role":"developer"}`))
		r.Host = "localhost"
		r.Header.Set("X-Citeck-CSRF", "1")
		r.Header.Set("X-Forwarded-For", "127.0.0.1")
		r.AddCookie(cookies[0])
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		require.Equal(t, 403, w.Code)
	}
	for _, invalid := range []string{
		`{"name":"` + strings.Repeat("x", 101) + `"}`,
		`{"expiresAt":"2000-01-01T00:00:00Z"}`,
		`{"expiresAt":"3000-01-01T00:00:00Z"}`,
		`{"neverExpires":true,"expiresAt":"2030-01-01T00:00:00Z"}`,
	} {
		refused := request(owner, "POST", "http://owner/api/v1/mcp/oauth/pending/"+id+"/approve", "application/json", invalid)
		require.Equal(t, 400, refused.StatusCode)
	}
	approval := `{}`
	if role == launchermcp.Developer {
		approval = `{"role":"developer","name":"Pi on Mac","neverExpires":true}`
	}
	approved := request(owner, "POST", "http://owner/api/v1/mcp/oauth/pending/"+id+"/approve", "application/json", approval)
	require.Equal(t, 200, approved.StatusCode)
	repeated := request(owner, "POST", "http://owner/api/v1/mcp/oauth/pending/"+id+"/approve", "application/json", `{"role":"developer"}`)
	require.Equal(t, 409, repeated.StatusCode)
	finish := func(csrfValue, origin string) *http.Response {
		t.Helper()
		r, e := http.NewRequest("POST", public.URL+cookies[0].Path+"/finish", strings.NewReader(url.Values{"csrf": {csrfValue}}.Encode()))
		require.NoError(t, e)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", origin)
		r.AddCookie(cookies[0])
		resp, e := httpClient.Do(r)
		require.NoError(t, e)
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}
	require.Equal(t, 403, finish(csrf[1], "http://attacker.example").StatusCode)
	require.Equal(t, 409, finish("wrong", public.URL).StatusCode)
	completed := finish(csrf[1], public.URL)
	require.Equal(t, 303, completed.StatusCode)
	location, err := url.Parse(completed.Header.Get("Location"))
	require.NoError(t, err)
	require.Equal(t, callback, strings.Split(completed.Header.Get("Location"), "?")[0])
	require.Equal(t, public.URL, location.Query().Get("iss"))
	require.Equal(t, auth.Get("state"), location.Query().Get("state"))
	require.Equal(t, 409, finish(csrf[1], public.URL).StatusCode)
	cfg := oauth2.Config{ClientID: registration.ID, RedirectURL: callback, Endpoint: oauth2.Endpoint{TokenURL: metadata["token_endpoint"].(string), AuthStyle: oauth2.AuthStyleInParams}}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)
	token, err := cfg.Exchange(ctx, location.Query().Get("code"), oauth2.VerifierOption(verifier), oauth2.SetAuthURLParam("resource", endpoint), oauth2.SetAuthURLParam("sdk_extension", "ignored"))
	require.NoError(t, err)
	require.NotEmpty(t, token.RefreshToken)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-Pi", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: &http.Client{Transport: mcpBearer{token.AccessToken}}}, nil)
	require.NoError(t, err)
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "namespace_status", Arguments: map[string]any{"workspaceId": "alpha", "namespaceId": "same"}})
	require.NoError(t, err)
	require.False(t, result.IsError)
	list, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	hasJVM := false
	for _, tool := range list.Tools {
		hasJVM = hasJVM || tool.Name == "jvm_command"
	}
	require.Equal(t, role == launchermcp.Developer, hasJVM, "the trusted owner's role alone selects tools")
	var connections struct {
		Connections []launchermcp.OAuthConnection `json:"connections"`
	}
	decode(request(owner, "GET", "http://owner/api/v1/mcp/oauth/connections", "", ""), &connections)
	require.Len(t, connections.Connections, 1)
	grant := connections.Connections[0]
	require.Equal(t, role, grant.Role)
	if role == launchermcp.Developer {
		require.Equal(t, "Pi on Mac", grant.Name)
		require.True(t, grant.NeverExpires)
	} else {
		require.False(t, grant.NeverExpires)
	}
	require.NotNil(t, grant.LastUsedAt)
	file, err := os.CreateTemp(t.TempDir(), "artifact")
	require.NoError(t, err)
	_, err = file.WriteString("diagnostic fixture")
	require.NoError(t, err)
	artifact, err := d.mcp.RegisterArtifact("oauth:"+grant.ID, "fixture.txt", file, nil, false)
	require.NoError(t, err)
	artifactStatus := func(value string) int {
		t.Helper()
		r, e := http.NewRequest("GET", public.URL+artifact.URL, http.NoBody)
		require.NoError(t, e)
		r.Header.Set("Authorization", "Bearer "+value)
		resp, e := httpClient.Do(r)
		require.NoError(t, e)
		defer resp.Body.Close()
		return resp.StatusCode
	}
	// x/oauth2 refresh omits resource; a Pi-style explicit-resource refresh follows.
	token.Expiry = time.Now().Add(-time.Hour)
	refreshed, err := cfg.TokenSource(ctx, token).Token()
	require.NoError(t, err)
	require.NotEqual(t, token.RefreshToken, refreshed.RefreshToken)
	expectedArtifact := 403
	if role == launchermcp.Developer {
		expectedArtifact = 200
	}
	require.Equal(t, expectedArtifact, artifactStatus(refreshed.AccessToken))
	var explicit oauth2.Token
	next := request(httpClient, "POST", cfg.Endpoint.TokenURL, "application/x-www-form-urlencoded", url.Values{"grant_type": {"refresh_token"}, "client_id": {registration.ID}, "refresh_token": {refreshed.RefreshToken}, "resource": {endpoint}}.Encode())
	require.Equal(t, 200, next.StatusCode)
	decode(next, &explicit)
	require.Equal(t, expectedArtifact, artifactStatus(explicit.AccessToken))
	revoked := request(httpClient, "POST", metadata["revocation_endpoint"].(string), "application/x-www-form-urlencoded", url.Values{"client_id": {registration.ID}, "token": {explicit.RefreshToken}, "sdk_extension": {"ignored"}}.Encode())
	require.Equal(t, 200, revoked.StatusCode)
	require.Equal(t, 401, artifactStatus(explicit.AccessToken))
	deny := request(owner, "DELETE", "http://owner/api/v1/mcp/oauth/connections/"+grant.ID, "", "")
	require.Equal(t, 200, deny.StatusCode)
	// A second ceremony is refused by the same trusted owner handler.
	deniedPage := request(httpClient, "GET", metadata["authorization_endpoint"].(string)+"?"+auth.Encode(), "", "")
	require.Equal(t, 200, deniedPage.StatusCode)
	deniedHTML, err := io.ReadAll(deniedPage.Body)
	require.NoError(t, err)
	cookies = deniedPage.Cookies()
	require.Len(t, cookies, 1)
	csrf = regexp.MustCompile(`name="csrf" value="([a-f0-9]{64})"`).FindStringSubmatch(string(deniedHTML))
	require.Len(t, csrf, 2)
	deniedID := strings.TrimPrefix(cookies[0].Path, "/oauth/requests/")
	denied := request(owner, "POST", "http://owner/api/v1/mcp/oauth/pending/"+deniedID+"/deny", "", "")
	require.Equal(t, 200, denied.StatusCode)
	deniedCallback := finish(csrf[1], public.URL)
	require.Equal(t, 303, deniedCallback.StatusCode)
	deniedURL, err := url.Parse(deniedCallback.Header.Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "access_denied", deniedURL.Query().Get("error"))
	require.Empty(t, deniedURL.Query().Get("code"))

}
