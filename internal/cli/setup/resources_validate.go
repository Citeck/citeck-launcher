package setup

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/citeck/citeck-launcher/internal/i18n"
)

// Recommended JVM overhead beyond heap in bytes (metaspace + direct buffers +
// thread stacks). Rule of thumb used to flag risky heap/memoryLimit pairings.
const jvmOverheadBytes int64 = 200 * 1024 * 1024

// parseMemSpec parses a strict memory size spec of the form <number>[m|M|g|G].
// Decimal values are allowed (e.g. "1.5g"). Returns the size in bytes. Returns
// an error for any other format (empty string, missing suffix, unsupported
// units like "k", negative numbers, trailing whitespace, etc.).
func parseMemSpec(s string) (int64, error) {
	if s == "" {
		return 0, errors.New("empty")
	}
	// Reject leading/trailing whitespace — callers should trim beforehand.
	if strings.TrimSpace(s) != s {
		return 0, errors.New("whitespace")
	}
	last := s[len(s)-1]
	var multiplier int64
	switch last {
	case 'm', 'M':
		multiplier = 1024 * 1024
	case 'g', 'G':
		multiplier = 1024 * 1024 * 1024
	default:
		return 0, fmt.Errorf("missing unit (expected m or g)")
	}
	num := s[:len(s)-1]
	if num == "" {
		return 0, errors.New("missing number")
	}
	// Reject signs explicitly; strconv.ParseFloat would accept them.
	if num[0] == '+' || num[0] == '-' {
		return 0, errors.New("sign not allowed")
	}
	v, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number: %w", err)
	}
	if v <= 0 {
		return 0, errors.New("must be positive")
	}
	return int64(v * float64(multiplier)), nil
}

// validateHeapFormat checks that a heap input string matches the accepted
// format. Returns a user-friendly i18n error on mismatch. An empty string is
// accepted (meaning "leave unchanged") — callers that require a value should
// check for emptiness separately.
func validateHeapFormat(s string) error {
	if s == "" {
		return nil
	}
	if _, err := parseMemSpec(s); err != nil {
		return errors.New(i18n.T("setup.resources.heap_format_err"))
	}
	return nil
}

// heapGuardResult describes the outcome of checking a heap value against a
// memoryLimit. Exactly one of Err / Warning is non-empty on a non-OK result.
type heapGuardResult struct {
	// Err is a hard-failure message (heap >= memLimit).
	Err string
	// Warning is an advisory message (heap within jvmOverheadBytes of memLimit).
	Warning string
}

// OK reports whether the pairing is acceptable with no user interaction.
func (r heapGuardResult) OK() bool { return r.Err == "" && r.Warning == "" }

// checkHeapVsMem compares a heap size against a container memory limit and
// returns a guard result. Empty strings skip the check (treated as "not set").
// Any parse error on either side also skips the guard (format validation is
// performed separately via validateHeapFormat / parseMemSpec).
func checkHeapVsMem(heap, mem string) heapGuardResult {
	if heap == "" || mem == "" {
		return heapGuardResult{}
	}
	heapBytes, err := parseMemSpec(heap)
	if err != nil {
		return heapGuardResult{}
	}
	memBytes, err := parseMemSpec(mem)
	if err != nil {
		return heapGuardResult{}
	}
	if memBytes <= 0 {
		return heapGuardResult{}
	}
	if heapBytes >= memBytes {
		return heapGuardResult{Err: i18n.T("setup.resources.heap_ge_mem_err", "heap", heap, "mem", mem)}
	}
	if heapBytes+jvmOverheadBytes > memBytes {
		return heapGuardResult{Warning: i18n.T("setup.resources.heap_close_mem_warn", "heap", heap, "mem", mem)}
	}
	return heapGuardResult{}
}
