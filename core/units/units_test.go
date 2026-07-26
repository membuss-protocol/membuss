package units

import (
	"testing"
)

func TestParseByteSize(t *testing.T) {
	tests := []struct {
		input    string
		expected uint64
		hasError bool
	}{
		{"0", 0, false},
		{"", 0, false},
		{"500KB", 500 * KB, false},
		{"500kb", 500 * KB, false},
		{"500 kb", 500 * KB, false},
		{"100MB", 100 * MB, false},
		{"100mb", 100 * MB, false},
		{"10GB", 10 * GB, false},
		{"10.5GB", uint64(10.5 * float64(GB)), false},
		{"1TB", 1 * TB, false},
		{"1tb", 1 * TB, false},
		{"107374182400", 107374182400, false},
		{"invalid", 0, true},
		{"100XY", 0, true},
	}

	for _, tt := range tests {
		got, err := ParseByteSize(tt.input)
		if (err != nil) != tt.hasError {
			t.Errorf("ParseByteSize(%q) error = %v, wantErr %v", tt.input, err, tt.hasError)
			continue
		}
		if !tt.hasError && got != tt.expected {
			t.Errorf("ParseByteSize(%q) = %d, want %d", tt.input, got, tt.expected)
		}
	}
}

func TestFormatByteSize(t *testing.T) {
	if FormatByteSize(0) != "0 B (unlimited)" {
		t.Errorf("expected 0 B (unlimited), got %s", FormatByteSize(0))
	}
	if FormatByteSize(100*MB) != "100.00 MB" {
		t.Errorf("expected 100.00 MB, got %s", FormatByteSize(100*MB))
	}
	if FormatByteSize(10*GB) != "10.00 GB" {
		t.Errorf("expected 10.00 GB, got %s", FormatByteSize(10*GB))
	}
}
