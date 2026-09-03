package main

import (
	"strconv"
	"strings"
)

// SemVer represents a parsed semantic version with optional pre-release identifier.
type SemVer struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease string // e.g. "beta.1", "alpha.2", "rc.1"
}

// ParseSemVer parses a version string like "v2.10.0-beta.1" or "2.9.4" into a SemVer struct.
func ParseSemVer(v string) SemVer {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")

	var sv SemVer
	if v == "" {
		return sv
	}

	// Separate core version from pre-release
	core := v
	if idx := strings.Index(v, "-"); idx >= 0 {
		core = v[:idx]
		sv.Prerelease = strings.TrimSpace(v[idx+1:])
	}

	parts := strings.Split(core, ".")
	if len(parts) > 0 {
		sv.Major, _ = strconv.Atoi(parts[0])
	}
	if len(parts) > 1 {
		sv.Minor, _ = strconv.Atoi(parts[1])
	}
	if len(parts) > 2 {
		sv.Patch, _ = strconv.Atoi(parts[2])
	}

	return sv
}

// CompareSemVer compares two semantic version strings.
// Returns -1 if v1 < v2, 0 if v1 == v2, 1 if v1 > v2.
//
// Rules adhere to Semantic Versioning 2.0.0:
// 1. Major, minor, and patch are compared numerically.
// 2. A version without a pre-release has higher precedence than one with a pre-release for the same core version (e.g. 2.10.0 > 2.10.0-beta.1).
// 3. Pre-releases are compared dot-by-dot, numerically when numeric, and lexically when alphanumeric.
func CompareSemVer(v1, v2 string) int {
	sv1 := ParseSemVer(v1)
	sv2 := ParseSemVer(v2)

	if sv1.Major != sv2.Major {
		if sv1.Major < sv2.Major {
			return -1
		}
		return 1
	}
	if sv1.Minor != sv2.Minor {
		if sv1.Minor < sv2.Minor {
			return -1
		}
		return 1
	}
	if sv1.Patch != sv2.Patch {
		if sv1.Patch < sv2.Patch {
			return -1
		}
		return 1
	}

	// Core versions are identical.
	// Rule: normal version > pre-release version.
	if sv1.Prerelease == "" && sv2.Prerelease == "" {
		return 0
	}
	if sv1.Prerelease == "" && sv2.Prerelease != "" {
		return 1 // sv1 is stable release, sv2 is pre-release
	}
	if sv1.Prerelease != "" && sv2.Prerelease == "" {
		return -1 // sv1 is pre-release, sv2 is stable release
	}

	// Both have pre-release strings: compare sub-identifiers
	p1Parts := strings.Split(sv1.Prerelease, ".")
	p2Parts := strings.Split(sv2.Prerelease, ".")

	for i := 0; i < len(p1Parts) && i < len(p2Parts); i++ {
		p1 := p1Parts[i]
		p2 := p2Parts[i]

		n1, err1 := strconv.Atoi(p1)
		n2, err2 := strconv.Atoi(p2)

		if err1 == nil && err2 == nil {
			if n1 != n2 {
				if n1 < n2 {
					return -1
				}
				return 1
			}
		} else if err1 == nil && err2 != nil {
			// Numeric identifiers have lower precedence than alphanumeric
			return -1
		} else if err1 != nil && err2 == nil {
			return 1
		} else {
			// Both alphanumeric: lexical comparison
			if p1 != p2 {
				if p1 < p2 {
					return -1
				}
				return 1
			}
		}
	}

	if len(p1Parts) < len(p2Parts) {
		return -1
	}
	if len(p1Parts) > len(p2Parts) {
		return 1
	}

	return 0
}

// IsVersionNewer returns true if candidate is strictly newer than current.
func IsVersionNewer(current, candidate string) bool {
	return CompareSemVer(current, candidate) < 0
}

// IsPrerelease returns true if the tag or version contains a pre-release identifier.
func IsPrerelease(tag string) bool {
	tag = strings.ToLower(strings.TrimSpace(tag))
	return strings.Contains(tag, "-beta") || strings.Contains(tag, "-alpha") || strings.Contains(tag, "-rc") || strings.Contains(tag, "-preview") || strings.Contains(tag, "-dev")
}
