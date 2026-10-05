package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// The slice of the ECOS Records API the content-storage connection speaks:
// load attributes of known records, and create or update one record. Spoken
// to ecos-gateway DIRECTLY — the daemon is not a browser, so it does not go
// through the proxy's OIDC — with the gateway's trusted identity header.

// ecosRecords is the seam between the provisioner's decisions and the wire.
// The provisioner is tested against a fake; httpEcosRecords is tested against
// an httptest server for the exact bytes it sends.
type ecosRecords interface {
	// Query loads the given attributes (alias -> attribute) of each ref and
	// answers one alias -> raw JSON value map per ref, in the order asked.
	Query(ctx context.Context, refs []string, atts map[string]string) ([]map[string]json.RawMessage, error)
	// Mutate creates (an empty local id: "emodel/secret@") or updates one
	// record and answers the id ECOS gave it.
	Mutate(ctx context.Context, id string, atts map[string]any) (string, error)
}

const (
	ecosRecordsQueryPath  = "/api/records/query"
	ecosRecordsMutatePath = "/api/records/mutate"
	// ecosRecordsUserHeader is the identity ecos-gateway trusts as given — the
	// header the proxy writes after authenticating a browser. The daemon
	// reaches the gateway on the namespace network (or its loopback-published
	// port on a desktop), never from outside.
	ecosRecordsUserHeader = "X-ECOS-User"
	ecosRecordsUser       = "admin"
	// ecosRecordsTimeout bounds one request. The calls are small; a gateway
	// that has not answered in this long is not ready, and the readiness poll
	// asks again.
	ecosRecordsTimeout = 30 * time.Second
	// ecosRecordsMaxBody bounds what is read from one answer.
	ecosRecordsMaxBody = 4 << 20
)

// ecosRecordsHTTP is the transport. Proxy deliberately unset: the address is a
// container IP or loopback, and an HTTP_PROXY in the daemon's environment must
// not route a call onto the namespace network through somebody's proxy.
var ecosRecordsHTTP = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        4,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     90 * time.Second,
	},
}

// httpEcosRecords is ecosRecords over HTTP against one gateway address.
type httpEcosRecords struct {
	base   string
	client *http.Client
}

// newHTTPEcosRecords answers the client for a gateway at host:port.
func newHTTPEcosRecords(addr string) ecosRecords {
	return &httpEcosRecords{base: "http://" + addr, client: ecosRecordsHTTP}
}

// ecosRecordsError is a failed records call. The two shapes worth telling
// apart are "the platform is not up yet" (ask again) and "this will not work"
// (say so), which is what retryable answers.
type ecosRecordsError struct {
	// Status is the HTTP status; 0 when no answer arrived at all.
	Status int
	// Messages are the ERROR-level messages of the answer, as text.
	Messages []string
	// Err is the transport or decoding failure, or the body of an answer that
	// carried no records messages.
	Err error
}

func (e *ecosRecordsError) Error() string {
	switch {
	case len(e.Messages) > 0:
		return fmt.Sprintf("records API answered HTTP %d: %s", e.Status, strings.Join(e.Messages, "; "))
	case e.Status == 0:
		return fmt.Sprintf("records API unreachable: %v", e.Err)
	default:
		return fmt.Sprintf("records API answered HTTP %d: %v", e.Status, e.Err)
	}
}

func (e *ecosRecordsError) Unwrap() error { return e.Err }

// retryable reports whether asking again later can change the answer. A
// platform that is still starting answers in every way but a client error:
// no connection, a 5xx (the gateway answers 500 whenever a message is an
// ERROR — "User 'admin' is not created yet", a records source that is not
// registered yet), a 404 from a route not mapped yet. A 4xx otherwise — the
// gateway refusing the identity, a request it cannot parse — is not going to
// change by itself.
func (e *ecosRecordsError) retryable() bool {
	switch {
	case e.Status == 0, e.Status >= 500, e.Status < 400:
		return true
	case e.Status == http.StatusNotFound, e.Status == http.StatusRequestTimeout,
		e.Status == http.StatusTooEarly, e.Status == http.StatusTooManyRequests:
		return true
	default:
		return false
	}
}

// ecosRecordsResponse is the part of a query/mutate answer that is read.
type ecosRecordsResponse struct {
	Records []struct {
		ID         string                     `json:"id"`
		Attributes map[string]json.RawMessage `json:"attributes"`
	} `json:"records"`
	Messages []ecosRecordsMessage `json:"messages"`
}

// ecosRecordsMessage is one entry of an answer's messages[]. Msg is either a
// plain string or a records error object ({"type", "msg", "stackTrace"}).
type ecosRecordsMessage struct {
	Level string          `json:"level"`
	Type  string          `json:"type"`
	Msg   json.RawMessage `json:"msg"`
}

func (m ecosRecordsMessage) text() string {
	var s string
	if json.Unmarshal(m.Msg, &s) == nil && s != "" {
		return s
	}
	var obj struct {
		Type string `json:"type"`
		Msg  string `json:"msg"`
	}
	if json.Unmarshal(m.Msg, &obj) == nil && obj.Msg != "" {
		if obj.Type != "" {
			return obj.Type + ": " + obj.Msg
		}
		return obj.Msg
	}
	return truncateText(strings.TrimSpace(string(m.Msg)), 300)
}

func (r *ecosRecordsResponse) errorMessages() []string {
	var out []string
	for _, m := range r.Messages {
		if strings.EqualFold(m.Level, "ERROR") {
			out = append(out, m.text())
		}
	}
	return out
}

// Query sends the records API v2 attributes query (the gateway's default
// version when the body names none): {"records": [...], "attributes": {...}}.
func (c *httpEcosRecords) Query(ctx context.Context, refs []string, atts map[string]string) ([]map[string]json.RawMessage, error) {
	body := struct {
		Records    []string          `json:"records"`
		Attributes map[string]string `json:"attributes"`
	}{Records: refs, Attributes: atts}
	var resp ecosRecordsResponse
	if err := c.post(ctx, ecosRecordsQueryPath, body, &resp); err != nil {
		return nil, err
	}
	if len(resp.Records) != len(refs) {
		return nil, &ecosRecordsError{Status: http.StatusOK,
			Err: fmt.Errorf("asked for %d records, got %d", len(refs), len(resp.Records))}
	}
	out := make([]map[string]json.RawMessage, len(resp.Records))
	for i, rec := range resp.Records {
		out[i] = rec.Attributes
		if out[i] == nil {
			out[i] = map[string]json.RawMessage{}
		}
	}
	return out, nil
}

// Mutate sends the records API v1 mutation of one record (the gateway reads
// every mutation as v1): {"records": [{"id": ..., "attributes": {...}}]}.
func (c *httpEcosRecords) Mutate(ctx context.Context, id string, atts map[string]any) (string, error) {
	type record struct {
		ID         string         `json:"id"`
		Attributes map[string]any `json:"attributes"`
	}
	body := struct {
		Records []record `json:"records"`
	}{Records: []record{{ID: id, Attributes: atts}}}
	var resp ecosRecordsResponse
	if err := c.post(ctx, ecosRecordsMutatePath, body, &resp); err != nil {
		return "", err
	}
	if len(resp.Records) != 1 || resp.Records[0].ID == "" {
		return "", &ecosRecordsError{Status: http.StatusOK,
			Err: fmt.Errorf("mutation of %s answered %d records", id, len(resp.Records))}
	}
	return resp.Records[0].ID, nil
}

// post sends one records request and decodes its answer. An answer is a
// failure when it is not 2xx or carries an ERROR message — the gateway pairs
// the two (HTTP 500 whenever a message is an ERROR), and either alone is
// treated the same way.
func (c *httpEcosRecords) post(ctx context.Context, path string, body any, out *ecosRecordsResponse) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode records request: %w", err)
	}
	reqCtx, cancel := context.WithTimeout(ctx, ecosRecordsTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.base+path, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("build records request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set(ecosRecordsUserHeader, ecosRecordsUser)
	resp, err := c.client.Do(req) //nolint:gosec // G704: the gateway's own address, resolved from the namespace's container (Runtime.AppHTTPAddress)
	if err != nil {
		return &ecosRecordsError{Err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, ecosRecordsMaxBody))
	if err != nil {
		return &ecosRecordsError{Status: resp.StatusCode, Err: fmt.Errorf("read answer: %w", err)}
	}
	decodeErr := json.Unmarshal(data, out)
	msgs := out.errorMessages()
	if resp.StatusCode < 200 || resp.StatusCode > 299 || len(msgs) > 0 {
		e := &ecosRecordsError{Status: resp.StatusCode, Messages: msgs}
		if len(msgs) == 0 {
			e.Err = errors.New(answerText(data))
		}
		return e
	}
	if decodeErr != nil {
		return &ecosRecordsError{Status: resp.StatusCode, Err: fmt.Errorf("decode answer: %w", decodeErr)}
	}
	return nil
}

// answerText is what an answer with no records messages says: the "message"
// of a Spring error body when there is one, else the start of the body.
func answerText(data []byte) string {
	var spring struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(data, &spring) == nil {
		switch {
		case spring.Message != "":
			return truncateText(spring.Message, 300)
		case spring.Error != "":
			return truncateText(spring.Error, 300)
		}
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "empty answer"
	}
	return truncateText(text, 300)
}

// truncateText cuts s to at most n bytes on a rune boundary, marking the cut.
func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
