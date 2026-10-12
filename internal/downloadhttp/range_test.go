package downloadhttp

import (
	"net/http"
	"testing"
)

func TestValidatePartialResponse(t *testing.T) {
	for _, tc := range []struct {
		name, header   string
		offset, length int64
		valid          bool
	}{
		{"known", "bytes 3-5/6", 3, 3, true},
		{"unknown", "bytes 3-5/*", 3, -1, true},
		{"wrong offset", "bytes 0-2/6", 3, 3, false},
		{"wrong length", "bytes 3-5/6", 3, 2, false},
		{"negative total", "bytes 3-5/-1", 3, 3, false},
		{"overflow span", "bytes 0-9223372036854775807/*", 0, -1, false},
		{"backward", "bytes 5-3/6", 5, -1, false},
		{"total too small", "bytes 3-5/5", 3, 3, false},
		{"missing", "", 3, -1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header := make(http.Header)
			header.Set("Content-Range", tc.header)
			span, err := ValidatePartialResponse(&http.Response{Header: header, ContentLength: tc.length}, tc.offset)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if tc.valid && (span.Start != 3 || span.End != 5) {
				t.Fatalf("unexpected span: %+v", span)
			}
			if tc.name == "unknown" && span.Total != -1 {
				t.Fatal("unknown total was fabricated")
			}
		})
	}
}
