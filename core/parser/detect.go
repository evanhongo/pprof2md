package parser

import "strings"

// ProfileType is the high-level category of a profile.
type ProfileType string

const (
	ProfileCPU       ProfileType = "cpu"
	ProfileHeap      ProfileType = "heap"
	ProfileMutex     ProfileType = "mutex"
	ProfileGoroutine ProfileType = "goroutine"
	ProfileUnknown   ProfileType = "unknown"
)

// DetectProfileType infers the profile category from its sample type names and
// units, mirroring the heuristics of the reference implementation.
func DetectProfileType(p *Profile) ProfileType {
	types := make([]string, len(p.SampleTypes))
	units := make([]string, len(p.SampleTypes))
	for i, st := range p.SampleTypes {
		types[i] = strings.ToLower(st.Type)
		units[i] = strings.ToLower(st.Unit)
	}

	// CPU profiles have time: cpu in Go, wall in V8 time profiles.
	if containsAny(types, "cpu", "wall") {
		return ProfileCPU
	}
	// Other CPU profiles have only sample counts.
	if containsAny(types, "samples", "sample") {
		return ProfileCPU
	}

	// Heap profiles: memory-related sample types.
	if containsAny(types, "alloc_objects", "alloc_space", "inuse_objects", "inuse_space", "objects", "space") {
		return ProfileHeap
	}

	// Go's mutex and block profiles: their delay is in nanoseconds, so this
	// comes before the unit-based fallback.
	if containsAny(types, "contentions", "delay") {
		return ProfileMutex
	}

	// Fall back to unit-based detection.
	if containsAny(units, "nanoseconds", "microseconds") {
		return ProfileCPU
	}
	if contains(units, "bytes") && containsSubstring(types, "object") {
		return ProfileHeap
	}

	// Go's goroutine profile names its sample type goroutine.
	if containsAny(types, "goroutine", "goroutines", "count") {
		return ProfileGoroutine
	}

	return ProfileUnknown
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func containsAny(haystack []string, needles ...string) bool {
	for _, n := range needles {
		if contains(haystack, n) {
			return true
		}
	}
	return false
}

func containsSubstring(haystack []string, sub string) bool {
	for _, s := range haystack {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
