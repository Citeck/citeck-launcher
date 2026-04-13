package setup

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMemSpec(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    int64
		wantErr bool
	}{
		{"megabytes lowercase", "512m", 512 * 1024 * 1024, false},
		{"megabytes uppercase", "2048M", 2048 * 1024 * 1024, false},
		{"gigabytes lowercase", "2g", 2 * 1024 * 1024 * 1024, false},
		{"gigabytes uppercase", "1G", 1 * 1024 * 1024 * 1024, false},
		{"decimal gigabytes", "1.5g", int64(1.5 * float64(1024*1024*1024)), false},
		{"decimal megabytes", "1.25m", int64(1.25 * float64(1024*1024)), false},

		{"empty", "", 0, true},
		{"letters only", "abc", 0, true},
		{"number without unit", "512", 0, true},
		{"kilobytes not allowed", "512k", 0, true},
		{"bytes not allowed", "512b", 0, true},
		{"negative value", "-5m", 0, true},
		{"positive sign", "+5m", 0, true},
		{"zero", "0m", 0, true},
		{"leading whitespace", " 512m", 0, true},
		{"trailing whitespace", "512m ", 0, true},
		{"unit only", "m", 0, true},
		{"double unit", "512mm", 0, true},
		{"wrong order", "m512", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseMemSpec(tc.in)
			if tc.wantErr {
				assert.Error(t, err, "expected error for %q", tc.in)
				return
			}
			require.NoError(t, err, "input %q", tc.in)
			assert.Equal(t, tc.want, got, "input %q", tc.in)
		})
	}
}

func TestValidateHeapFormat(t *testing.T) {
	// Empty is accepted as "leave unchanged".
	assert.NoError(t, validateHeapFormat(""))

	// Valid forms.
	assert.NoError(t, validateHeapFormat("512m"))
	assert.NoError(t, validateHeapFormat("2g"))
	assert.NoError(t, validateHeapFormat("1.5G"))
	assert.NoError(t, validateHeapFormat("2048M"))

	// Invalid forms surface an error regardless of locale (i18n fallback
	// returns the key or a localized string; either way it's non-nil).
	for _, bad := range []string{"abc", "512", "-5m", "0m", "512k", " 2g"} {
		assert.Error(t, validateHeapFormat(bad), "expected format error for %q", bad)
	}
}

func TestCheckHeapVsMem_HardFail(t *testing.T) {
	// Heap equal to or above memory limit → hard error.
	r := checkHeapVsMem("1200m", "1000m")
	assert.NotEmpty(t, r.Err, "expected hard error")
	assert.Empty(t, r.Warning)
	assert.False(t, r.OK())

	r = checkHeapVsMem("1g", "1g")
	assert.NotEmpty(t, r.Err, "expected hard error for equal values")

	r = checkHeapVsMem("2g", "1g")
	assert.NotEmpty(t, r.Err, "expected hard error when heap > mem")
}

func TestCheckHeapVsMem_Warning(t *testing.T) {
	// Heap below mem but within the 200 MB JVM overhead margin → warning.
	r := checkHeapVsMem("1100m", "1200m")
	assert.Empty(t, r.Err)
	assert.NotEmpty(t, r.Warning, "expected warning")
	assert.False(t, r.OK())

	// Exactly 200 MB margin → not enough (warning fires when margin < 200m,
	// i.e. heap + 200m > mem; 1000m + 200m == 1200m, so OK).
	r = checkHeapVsMem("1000m", "1200m")
	assert.True(t, r.OK(), "exactly 200 MB margin should be OK")

	// 199 MB margin → warning.
	r = checkHeapVsMem("1001m", "1200m")
	assert.NotEmpty(t, r.Warning)
}

func TestCheckHeapVsMem_OK(t *testing.T) {
	r := checkHeapVsMem("1024m", "2048m")
	assert.True(t, r.OK())

	r = checkHeapVsMem("512m", "2g")
	assert.True(t, r.OK())
}

func TestCheckHeapVsMem_NoMemLimit(t *testing.T) {
	// Empty memLimit → guard skipped (no warning, no error).
	assert.True(t, checkHeapVsMem("2g", "").OK())
	// Empty heap → guard skipped.
	assert.True(t, checkHeapVsMem("", "1g").OK())
	// Both empty → guard skipped.
	assert.True(t, checkHeapVsMem("", "").OK())
}

func TestCheckHeapVsMem_UnparseableSkipped(t *testing.T) {
	// If either side fails to parse, the format validator handles it
	// separately; the guard skips rather than double-reporting.
	assert.True(t, checkHeapVsMem("bogus", "1g").OK())
	assert.True(t, checkHeapVsMem("1g", "bogus").OK())
}
