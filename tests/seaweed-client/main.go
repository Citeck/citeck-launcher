// Seaweed integration tests execute this static client inside temporary
// containers, where loopback works on rootless Docker and Docker Desktop.
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func mac(key []byte, value string) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(value))
	return h.Sum(nil)
}
func run() error {
	if len(os.Args) != 3 {
		return fmt.Errorf("expected method and S3 path")
	}
	body, err := os.ReadFile("/citeck/seaweed-test/request.bin")
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	if !strings.HasPrefix(os.Args[2], "/") {
		return fmt.Errorf("S3 path must start with /")
	}
	req, err := http.NewRequest(os.Args[1], "http://127.0.0.1:8333"+os.Args[2], bytes.NewReader(body)) //nolint:gosec // G704: fixed loopback endpoint inside an isolated test container.
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	now := time.Now().UTC()
	stamp, date := now.Format("20060102T150405Z"), now.Format("20060102")
	payload := sha256.Sum256(body)
	digest := hex.EncodeToString(payload[:])
	req.Header.Set("X-Amz-Date", stamp)
	req.Header.Set("X-Amz-Content-Sha256", digest)
	query := strings.ReplaceAll(req.URL.Query().Encode(), "+", "%20")
	canonical := req.Method + "\n" + req.URL.EscapedPath() + "\n" + query + "\nhost:" + req.URL.Host + "\nx-amz-content-sha256:" + digest + "\nx-amz-date:" + stamp + "\n\nhost;x-amz-content-sha256;x-amz-date\n" + digest
	scope := date + "/us-east-1/s3/aws4_request"
	sum := sha256.Sum256([]byte(canonical))
	signing := mac(mac(mac(mac([]byte("AWS4"+os.Getenv("AWS_SECRET_ACCESS_KEY")), date), "us-east-1"), "s3"), "aws4_request")
	signature := hex.EncodeToString(mac(signing, "AWS4-HMAC-SHA256\n"+stamp+"\n"+scope+"\n"+hex.EncodeToString(sum[:])))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+os.Getenv("AWS_ACCESS_KEY_ID")+"/"+scope+", SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature="+signature)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req) //nolint:gosec // G704: only the fixed loopback test endpoint above is reachable.
	if err != nil {
		return fmt.Errorf("S3 request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read S3 response: %w", err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(struct {
		Status  int
		Headers http.Header
		Body    []byte
	}{resp.StatusCode, resp.Header, data}); err != nil {
		return fmt.Errorf("encode response: %w", err)
	}
	return nil
}
func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
