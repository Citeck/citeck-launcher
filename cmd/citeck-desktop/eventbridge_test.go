//go:build desktop

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventBridgeReplaysSnapshotCompletionAfterReconnect(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "text/event-stream")
		switch requests {
		case 1:
			assert.Empty(t, r.Header.Get("Last-Event-ID"))
			fmt.Fprint(w, "id: 41\ndata: {\"type\":\"snapshot_progress\",\"seq\":41}\n\nid: 42\ndata: {\"type\":\"snapshot_complete\"") // dropped halfway through frame
		case 2:
			assert.Equal(t, "41", r.Header.Get("Last-Event-ID"))
			fmt.Fprint(w, "id: 42\ndata: {\"type\":\"snapshot_complete\",\"seq\":42}\n\n")
		}
	}))
	defer srv.Close()
	// Use the production bridge URL through a local test transport.
	client := srv.Client()
	client.Transport = bridgeTestTransport{base: client.Transport, host: srv.Listener.Addr().String()}
	var received []string
	emit := func(name string, args ...any) bool {
		if name == wailsDaemonEvent {
			var data struct {
				Type string `json:"type"`
			}
			require.NoError(t, json.Unmarshal(args[0].(json.RawMessage), &data))
			received = append(received, data.Type)
		}
		return false
	}
	cursor := ""
	streamDaemonEventsOnce(context.Background(), emit, client, &cursor)
	require.Equal(t, []string{"snapshot_progress"}, received)
	require.Equal(t, "41", cursor, "partial frames must not move the replay cursor")
	streamDaemonEventsOnce(context.Background(), emit, client, &cursor)
	require.Equal(t, []string{"snapshot_progress", "snapshot_complete"}, received)
	require.Equal(t, "42", cursor)
}

type bridgeTestTransport struct {
	base http.RoundTripper
	host string
}

func (t bridgeTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	target := r.Clone(r.Context())
	target.URL.Scheme = "http"
	target.URL.Host = t.host
	response, err := t.base.RoundTrip(target)
	if err != nil {
		return nil, fmt.Errorf("test transport: %w", err)
	}
	return response, nil
}
