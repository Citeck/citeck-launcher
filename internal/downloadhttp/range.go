package downloadhttp

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// ByteRange describes an inclusive byte span; Total is -1 when unknown.
type ByteRange struct{ Start, End, Total int64 }

// ValidatePartialResponse checks that a 206 response covers the requested offset.
func ValidatePartialResponse(resp *http.Response, offset int64) (ByteRange, error) {
	header := resp.Header.Get("Content-Range")
	span, totalText, ok := strings.Cut(strings.TrimPrefix(header, "bytes "), "/")
	startText, endText, spanOK := strings.Cut(span, "-")
	start, startErr := strconv.ParseInt(startText, 10, 64)
	end, endErr := strconv.ParseInt(endText, 10, 64)
	total, totalErr := strconv.ParseInt(totalText, 10, 64)
	if totalText == "*" {
		total = -1
		totalErr = nil
	}
	if !strings.HasPrefix(header, "bytes ") || !ok || !spanOK || startErr != nil || endErr != nil || totalErr != nil || start < 0 || end < start || end-start == math.MaxInt64 || (totalText != "*" && total <= end) {
		return ByteRange{}, errors.New("invalid download Content-Range")
	}
	if start != offset || (resp.ContentLength >= 0 && resp.ContentLength != end-start+1) {
		return ByteRange{}, errors.New("download Content-Range does not match requested bytes")
	}
	return ByteRange{Start: start, End: end, Total: total}, nil
}

// ResumeValidator returns a strong ETag or Last-Modified for If-Range.
func ResumeValidator(header http.Header) string {
	validator := header.Get("ETag")
	if strings.HasPrefix(validator, "W/") {
		validator = ""
	}
	if validator == "" {
		validator = header.Get("Last-Modified")
	}
	return validator
}
