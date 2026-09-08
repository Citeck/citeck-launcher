package fsutil

import "fmt"

// FormatBytes renders a byte count in binary units for a human reader
// ("2.0 GiB", "512.0 MiB", "900 B"). Binary units, spelled out, because every
// number this formats is compared against a filesystem's free space or a
// Docker volume's size, both of which are reported in powers of 1024 — a
// "2.1 GB" next to a 2 GiB volume is the kind of mismatch that makes an
// operator distrust the whole message.
//
// Sizes below one unit are printed verbatim so a small (or nonsensically
// negative) measurement is never dressed up as a fraction of a kibibyte.
func FormatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
