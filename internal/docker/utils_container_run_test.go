package docker

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
)

func utilsTestClient(t *testing.T, running bool, output string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/images/"):
			_, _ = w.Write([]byte(`{"Id":"utils-image"}`))
		case strings.HasSuffix(r.URL.Path, "/containers/create"):
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"Id":"utils-test"}`))
		case strings.HasSuffix(r.URL.Path, "/json"):
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": "utils-test", "State": map[string]any{"Running": running, "ExitCode": 1}})
		case strings.HasSuffix(r.URL.Path, "/wait"):
			_, _ = w.Write([]byte(`{"StatusCode":1}`))
		case strings.HasSuffix(r.URL.Path, "/logs"):
			w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
			message := []byte(output)
			header := make([]byte, 8)
			header[0] = 2
			binary.BigEndian.PutUint32(header[4:], uint32(len(message)))
			_, _ = w.Write(append(header, message...))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)
	sdk, err := client.New(client.WithHost(srv.URL), client.WithAPIVersion("1.54"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sdk.Close() })
	return &Client{cli: sdk, namespace: "utils-test"}
}

// Exercise the full SDK path: a utils command that ran and failed must retain
// stderr and its exit code, both when already exited and when Wait observes it.
func TestUtilsCommandFailurePreservesOutput(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(fmt.Sprintf("running=%t", running), func(t *testing.T) {
			dc := utilsTestClient(t, running, "cat: /vol/18/docker/PG_VERSION: No such file or directory\n")
			output, code, err := dc.RunUtilsContainerWithTimeout(context.Background(), []string{"cat", "/vol/18/docker/PG_VERSION"}, nil, time.Second)
			require.NoError(t, err)
			require.Equal(t, 1, code)
			require.Contains(t, output, "No such file or directory")
			require.ErrorContains(t, dc.WaitForContainerExit(context.Background(), "utils-test", time.Second), "exited with code 1", "init containers must still require success")
		})
	}
}

func TestVolumeSizeRejectsPartialDuOutput(t *testing.T) {
	dc := utilsTestClient(t, false, "du: /vol/private: Permission denied\n12\t/vol\n")
	_, err := dc.VolumeSize(context.Background(), "data")
	require.ErrorContains(t, err, "exited with code 1", "an incomplete size is not a successful measurement")
}
