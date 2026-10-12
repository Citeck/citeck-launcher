package git

import (
	"net/url"
	"regexp"
	"strings"
)

// SafeURL removes userinfo and query/fragment data from repository diagnostics.
func SafeURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		if !strings.Contains(raw, "://") && strings.Contains(raw, "@") {
			return strings.SplitN(raw, "@", 2)[1]
		}
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

var diagnosticURLPattern = regexp.MustCompile(`(?:https?|ssh|git)://[^\s<>"']+`)

type diagnosticError struct {
	message  string
	original error
}

func (e *diagnosticError) Error() string { return e.message }
func (e *diagnosticError) Unwrap() error { return e.original }

// SafeError preserves error identity while removing URLs and sensitive values
// from its printable text. Sensitive values are only inspected in memory.
func SafeError(err error, repoURL string, sensitiveValues ...string) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if repoURL != "" {
		message = strings.ReplaceAll(message, repoURL, SafeURL(repoURL))
	}
	message = diagnosticURLPattern.ReplaceAllStringFunc(message, SafeURL)
	if parsed, parseErr := url.Parse(repoURL); parseErr == nil {
		if parsed.User != nil {
			sensitiveValues = append(sensitiveValues, parsed.User.Username())
			if password, ok := parsed.User.Password(); ok {
				sensitiveValues = append(sensitiveValues, password)
			}
		}
		for _, values := range parsed.Query() {
			sensitiveValues = append(sensitiveValues, values...)
		}
	}
	for _, value := range sensitiveValues {
		if value != "" {
			for _, encoded := range []string{value, url.QueryEscape(value), url.PathEscape(value)} {
				message = strings.ReplaceAll(message, encoded, "[redacted]")
			}
		}
	}
	if message == err.Error() {
		return err
	}
	return &diagnosticError{message: message, original: err}
}
