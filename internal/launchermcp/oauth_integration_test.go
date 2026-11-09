package launchermcp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestOAuthPrincipalArtifactsSurviveRotationAndDenyRevocationDebt(t *testing.T) {
	h := newOAuthTestCore(t)
	_, first := h.issue(t, Developer, time.Now().UTC())
	principal, ok := h.store.authenticatePrincipal(first.GetAccessToken())
	require.True(t, ok)
	service := New(h.store, &fakeBackend{})
	defer service.Close()
	path := filepath.Join(t.TempDir(), "heap.hprof")
	require.NoError(t, os.WriteFile(path, []byte("heap evidence"), 0o600))
	file, err := os.Open(path)
	require.NoError(t, err)
	artifact, err := service.RegisterArtifact(principal.artifactIdentity(), "heap.hprof", file, nil, false)
	require.NoError(t, err)
	handler := service.Handler(DefaultPort)
	get := func(value, host string) int {
		r := httptest.NewRequest(http.MethodGet, artifact.URL, http.NoBody)
		r.Host = host
		r.Header.Set("Authorization", "Bearer "+value)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	require.Equal(t, 200, get(first.GetAccessToken(), "127.0.0.1:19380"))
	require.Equal(t, 401, get(first.GetAccessToken(), "localhost:19380"))
	rotated, err := h.provider.exchange(context.Background(), h.refreshForm(testRefresh(first)))
	require.NoError(t, err)
	second, ok := h.store.authenticatePrincipal(rotated.GetAccessToken())
	require.True(t, ok)
	require.Equal(t, principal.artifactIdentity(), second.artifactIdentity())
	require.Equal(t, 200, get(rotated.GetAccessToken(), "127.0.0.1:19380"))
	_, other := h.issue(t, Developer, time.Now().UTC())
	require.Equal(t, 404, get(other.GetAccessToken(), "127.0.0.1:19380"))
	h.store.beforeCommit = func() error { return errors.New("disk refused") }
	require.Error(t, h.provider.revokeToken(context.Background(), url.Values{"client_id": {h.client.ID}, "token": {rotated.GetAccessToken()}}))
	require.Equal(t, 401, get(rotated.GetAccessToken(), "127.0.0.1:19380"))
	require.NotEmpty(t, service.Status().OAuthWarning)
}

func TestOAuthSettingsDisableRetainsGrantsAndPortChangeAtomicallyInvalidates(t *testing.T) {
	h := newOAuthTestCore(t)
	_, first := h.issue(t, Observer, time.Now().UTC())
	_, static, err := h.store.Create("permanent static", Developer, nil)
	require.NoError(t, err)
	code, err := h.provider.mintApprovedCode(context.Background(), h.authorizeForm(), Observer, time.Now().UTC())
	require.NoError(t, err)
	write := h.store.beforeCommit
	h.store.beforeCommit = func() error { return errors.New("disk refused") }
	require.Error(t, h.store.SetSettings(Settings{Enabled: true, Port: DefaultPort + 1}))
	_, err = h.provider.inspect(context.Background(), first.GetAccessToken())
	require.NoError(t, err)
	h.store.beforeCommit = write
	require.NoError(t, h.store.SetSettings(Settings{Enabled: false, Port: DefaultPort}))
	require.Empty(t, testSQLState(t, h.store).OAuth.Codes)
	require.Empty(t, testSQLState(t, h.store).OAuth.PKCE)
	require.NotEmpty(t, testSQLState(t, h.store).OAuth.Grants)
	connections, listErr := h.store.ListOAuthConnections(context.Background())
	require.NoError(t, listErr)
	revokedCount := 0
	for _, connection := range connections {
		if connection.RevokedAt != nil {
			revokedCount++
		}
	}
	require.Equal(t, 1, revokedCount, "the canceled code is visibly revoked; the issued connection remains durable")
	require.NoError(t, h.store.SetSettings(Settings{Enabled: true, Port: DefaultPort}))
	_, err = h.provider.exchange(context.Background(), h.exchangeForm(code))
	require.Error(t, err)
	_, err = h.provider.inspect(context.Background(), first.GetAccessToken())
	require.NoError(t, err)
	h = h.restart(t)
	_, err = h.provider.exchange(context.Background(), h.refreshForm(testRefresh(first)))
	require.NoError(t, err)
	require.NoError(t, h.store.SetSettings(Settings{Enabled: true, Port: DefaultPort + 1}))
	h = h.restart(t)
	require.NoError(t, h.store.SetSettings(Settings{Enabled: true, Port: DefaultPort}))
	_, err = h.provider.inspect(context.Background(), first.GetAccessToken())
	require.Error(t, err, "port ABA must not restore the epoch")
	_, ok := h.store.Authenticate(static)
	require.True(t, ok)
	_, err = h.provider.inspect(context.Background(), first.GetAccessToken())
	require.Error(t, err)
}

func TestOAuthConfigureFailedBindPreservesOldSettingsAndGrants(t *testing.T) {
	h := newOAuthTestCore(t)
	_, first := h.issue(t, Observer, time.Now().UTC())
	service := New(h.store, &fakeBackend{})
	defer service.Close()
	occupied, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	defer occupied.Close()
	_, portText, err := net.SplitHostPort(occupied.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	require.Error(t, service.Configure(Settings{Enabled: true, Port: port}))
	require.Equal(t, DefaultPort, h.store.Settings().Port)
	_, err = h.provider.inspect(context.Background(), first.GetAccessToken())
	require.NoError(t, err)
}

func TestOAuthGrantLastUsedIsResourceOnlyAndMinuteThrottledOnFailure(t *testing.T) {
	h := newOAuthTestCore(t)
	_, first := h.issue(t, Observer, time.Now().UTC())
	for _, g := range testSQLState(t, h.store).OAuth.Grants {
		require.Nil(t, g.LastUsedAt)
	}
	rotated, err := h.provider.exchange(context.Background(), h.refreshForm(testRefresh(first)))
	require.NoError(t, err)
	for _, g := range testSQLState(t, h.store).OAuth.Grants {
		require.Nil(t, g.LastUsedAt)
	}
	write := h.store.beforeCommit
	attempts := 0
	h.store.beforeCommit = func() error { attempts++; return errors.New("full") }
	for range 10 {
		_, ok := h.store.authenticatePrincipal(rotated.GetAccessToken())
		require.True(t, ok)
	}
	require.Equal(t, 1, attempts)
	h.store.beforeCommit = write
	h.store.mu.Lock()
	clear(h.store.lastUsedAttempt)
	h.store.mu.Unlock()
	_, ok := h.store.authenticatePrincipal(rotated.GetAccessToken())
	require.True(t, ok)
	for _, g := range testSQLState(t, h.store).OAuth.Grants {
		require.NotNil(t, g.LastUsedAt)
	}
}

func TestOAuthRetryOwnedWorkerRecoversWithoutAuthenticationAndJoins(t *testing.T) {
	h := newOAuthTestCore(t)
	_, first := h.issue(t, Observer, time.Now().UTC())
	write := h.store.beforeCommit
	var refused atomic.Bool
	refused.Store(true)
	h.store.beforeCommit = func() error {
		if refused.Load() {
			return errors.New("offline")
		}
		return func() error {
			if write != nil {
				return write()
			}
			return nil
		}()
	}
	require.Error(t, h.provider.revokeToken(context.Background(), url.Values{"client_id": {h.client.ID}, "token": {first.GetAccessToken()}}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); h.store.runOAuthRevocationRetry(ctx) }()
	refused.Store(false)
	require.Eventually(t, func() bool { return h.store.RevocationWarning() == "" }, 4*time.Second, 20*time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("retry worker did not join")
	}
	h = h.restart(t)
	_, err := h.provider.inspect(context.Background(), first.GetAccessToken())
	require.Error(t, err)
}

func TestOAuthServiceOwnsRetryLifecycle(t *testing.T) {
	store, err := OpenStore(testStorePath(t))
	require.NoError(t, err)
	service := New(store, &fakeBackend{})
	defer service.Close()
	done := service.retryDone
	require.NotNil(t, done, "default OFF still allows management revocations")
	require.False(t, service.Status().Listening)
	reservation, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	_, text, err := net.SplitHostPort(reservation.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(text)
	require.NoError(t, err)
	require.NoError(t, reservation.Close())
	require.NoError(t, service.Configure(Settings{Enabled: true, Port: port}))
	require.Equal(t, done, service.retryDone, "enabling must reuse the service worker")
	require.NoError(t, service.Configure(Settings{Enabled: false, Port: port}))
	select {
	case <-done:
		t.Fatal("disable abandoned pending revocation retry")
	default:
	}
	service.Close()
	select {
	case <-done:
	default:
		t.Fatal("Close returned before retry worker joined")
	}
}

func TestOAuthDisabledRestartRetriesManagementRevocation(t *testing.T) {
	h := newOAuthTestCore(t)
	_, first := h.issue(t, Observer, time.Now().UTC())
	connections, err := h.store.ListOAuthConnections(context.Background())
	require.NoError(t, err)
	require.Len(t, connections, 1)
	id := connections[0].ID
	require.NoError(t, h.store.SetSettings(Settings{Enabled: false, Port: DefaultPort}))
	h = h.restart(t)

	var refused atomic.Bool
	refused.Store(true)
	h.store.beforeCommit = func() error {
		if refused.Load() {
			return errors.New("storage temporarily unavailable")
		}
		return nil
	}
	service := New(h.store, &fakeBackend{})
	defer service.Close()
	require.NoError(t, service.Start())
	require.False(t, service.Status().Listening)
	require.Error(t, h.store.RevokeOAuthConnection(context.Background(), id))
	require.NotEmpty(t, h.store.RevocationWarning())
	refused.Store(false)
	require.Eventually(t, func() bool { return h.store.RevocationWarning() == "" }, 4*time.Second, 20*time.Millisecond)
	require.False(t, service.Status().Listening, "retry must never enable MCP")
	service.Close()

	h = h.restart(t)
	connections, err = h.store.ListOAuthConnections(context.Background())
	require.NoError(t, err)
	require.Len(t, connections, 1)
	require.True(t, connections[0].Revoked, "revocation must survive restart")
	require.NoError(t, h.store.SetSettings(Settings{Enabled: true, Port: DefaultPort}))
	_, err = h.provider.inspect(context.Background(), first.GetAccessToken())
	require.Error(t, err)
}

func TestOAuthStoreGCDoesNotEvictLiveReplayTombstones(t *testing.T) {
	h := newOAuthTestCore(t)
	code, first := h.issue(t, Observer, time.Now().UTC())
	_, err := h.provider.exchange(context.Background(), h.refreshForm(testRefresh(first)))
	require.NoError(t, err)
	require.NoError(t, h.store.oauthWrite(context.Background(), h.store.collectOAuth))
	require.Equal(t, 1, testCount(t, h.store, "oauth_refresh_used"))
	_, err = h.provider.exchange(context.Background(), h.exchangeForm(code))
	require.Error(t, err)
	_, err = h.provider.inspect(context.Background(), first.GetAccessToken())
	require.Error(t, err)
}

func TestOAuthPrincipalSDKResourceRoleMatrixUsesOwnerRole(t *testing.T) {
	for _, role := range []Role{Observer, Developer} {
		t.Run(string(role), func(t *testing.T) {
			server := httptest.NewUnstartedServer(nil)
			defer server.Close()
			_, text, err := net.SplitHostPort(server.Listener.Addr().String())
			require.NoError(t, err)
			port, err := strconv.Atoi(text)
			require.NoError(t, err)
			store, err := OpenStore(testStorePath(t))
			require.NoError(t, err)
			require.NoError(t, store.SetSettings(Settings{Enabled: true, Port: port}))
			client, err := store.oauth.registerClient(context.Background(), "unverified", []string{testOAuthCallback})
			require.NoError(t, err)
			h := &oauthTestCore{store: store, provider: store.oauth, client: client}
			_, credential := h.issue(t, role, time.Now().UTC())
			service := New(store, &fakeBackend{})
			defer service.Close()
			server.Config.Handler = service.Handler(port)
			server.Start()
			session := sdkSession(t, server.URL+"/mcp", credential.GetAccessToken())
			list, err := session.ListTools(context.Background(), nil)
			require.NoError(t, err)
			found := map[string]bool{}
			for _, tool := range list.Tools {
				found[tool.Name] = true
			}
			for _, contract := range expectedToolContracts {
				require.Equal(t, role == Developer || !contract.developer, found[contract.name], contract.name)
			}
			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "namespace_status", Arguments: map[string]any{"workspaceId": "ws", "namespaceId": "ns"}})
			require.NoError(t, err)
			require.False(t, result.IsError)
		})
	}
}
