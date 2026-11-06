package appdef

import (
	"errors"
	"math"
)

// NanoCPUs converts a finite core limit to Docker's billionths of a CPU.
// Zero is the legacy absence of a cap. Fractions below that precision and
// overflowing values are refused, never silently truncated.
func NanoCPUs(cores float64) (int64, error) {
	if cores == 0 {
		return 0, nil
	}
	n := cores * 1e9
	if math.IsNaN(n) || math.IsInf(n, 0) || n < 1 || n >= float64(math.MaxInt64) || math.Trunc(n) != n {
		return 0, errors.New("CPU limit must be positive, finite, representable in whole nano-CPUs and within int64 range")
	}
	return int64(n), nil
}
