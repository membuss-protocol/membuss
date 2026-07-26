package units

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

const (
	KB uint64 = 1024
	MB uint64 = 1024 * KB
	GB uint64 = 1024 * MB
	TB uint64 = 1024 * GB
	PB uint64 = 1024 * TB
)

// ParseByteSize converts a human-readable size string (e.g., "100MB", "10.5GB", "500KB", "1TB", "0")
// or plain byte number string into uint64 bytes.
func ParseByteSize(input string) (uint64, error) {
	s := strings.TrimSpace(input)
	if s == "" || s == "0" {
		return 0, nil
	}

	// Separate numeric value and unit suffix
	var numStr strings.Builder
	var unitStr strings.Builder

	for _, r := range s {
		if unicode.IsDigit(r) || r == '.' {
			numStr.WriteRune(r)
		} else if !unicode.IsSpace(r) {
			unitStr.WriteRune(r)
		}
	}

	valStr := numStr.String()
	if valStr == "" {
		return 0, fmt.Errorf("invalid byte size format: %q", input)
	}

	val, err := strconv.ParseFloat(valStr, 64)
	if err != nil || val < 0 {
		return 0, fmt.Errorf("invalid numeric value in byte size %q: %v", input, err)
	}

	unit := strings.ToUpper(strings.TrimSpace(unitStr.String()))
	var multiplier uint64 = 1

	switch unit {
	case "", "B", "BYTES", "BYTE":
		multiplier = 1
	case "K", "KB", "KIB", "KILOBYTES", "KILOBYTE":
		multiplier = KB
	case "M", "MB", "MIB", "MEGABYTES", "MEGABYTE":
		multiplier = MB
	case "G", "GB", "GIB", "GIGABYTES", "GIGABYTE":
		multiplier = GB
	case "T", "TB", "TIB", "TERABYTES", "TERABYTE":
		multiplier = TB
	case "P", "PB", "PIB", "PETABYTES", "PETABYTE":
		multiplier = PB
	default:
		return 0, fmt.Errorf("unknown size unit %q in %q (expected KB, MB, GB, TB, PB)", unit, input)
	}

	bytes := uint64(val * float64(multiplier))
	return bytes, nil
}

// FormatByteSize converts bytes to a clean human-readable string (e.g. "100.00 MB", "10.50 GB").
func FormatByteSize(bytes uint64) string {
	if bytes == 0 {
		return "0 B (unlimited)"
	}
	b := float64(bytes)
	switch {
	case bytes >= PB:
		return fmt.Sprintf("%.2f PB", b/float64(PB))
	case bytes >= TB:
		return fmt.Sprintf("%.2f TB", b/float64(TB))
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", b/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", b/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", b/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
