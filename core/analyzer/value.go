package analyzer

import (
	"slices"
	"strings"

	"pprof2md/core/parser"
)

// GetPrimarySampleTypeIndex picks the sample type to analyze: the profile's
// default type when it names one (Go's allocs profile names alloc_space),
// or else the most meaningful type for the profile category. For CPU that is
// time rather than sample counts, as pprof shows by default: the profile's
// CPU time, or else another time (V8 time profiles have only wall). Any
// other profile gets the last type, as in pprof: inuse_space for Go's heap
// profile, alloc_space for V8's heap profile with allocations, and delay for
// Go's mutex and block profiles.
func GetPrimarySampleTypeIndex(p *parser.Profile, pt parser.ProfileType) int {
	types := p.SampleTypes
	named := func(names ...string) int {
		for _, name := range names {
			if i := slices.IndexFunc(types, func(t parser.SampleType) bool { return strings.EqualFold(t.Type, name) }); i >= 0 {
				return i
			}
		}
		return -1
	}

	if p.DefaultSampleType != "" {
		if i := named(p.DefaultSampleType); i >= 0 {
			return i
		}
	}

	// timed picks the named time, or else any time, or else sample counts.
	timed := func(name string) int {
		i := named(name)
		if i < 0 {
			i = slices.IndexFunc(types, func(t parser.SampleType) bool { return isTimeUnit(t.Unit) })
		}
		if i < 0 {
			i = named("samples", "sample")
		}
		return i
	}

	i := -1
	if pt == parser.ProfileCPU {
		i = timed("cpu")
	}
	if i < 0 {
		i = len(types) - 1
	}
	return max(i, 0)
}

// samplesTypeIndex returns the index of the type that counts the samples
// each record stands for (samples/count in Go CPU profiles, sample/count in
// V8 time profiles), or -1 if the profile has none.
func samplesTypeIndex(p *parser.Profile) int {
	return slices.IndexFunc(p.SampleTypes, func(t parser.SampleType) bool {
		return (strings.EqualFold(t.Type, "samples") || strings.EqualFold(t.Type, "sample")) && strings.EqualFold(t.Unit, "count")
	})
}

// objectsTypeIndex returns the index of the object count that goes with the
// type at idx when that is a heap profile's bytes: alloc_objects for
// alloc_space, inuse_objects for inuse_space, objects for V8's space. It
// returns -1 for any other type, or when the profile has no such count.
func objectsTypeIndex(p *parser.Profile, idx int) int {
	st := p.SampleTypes[idx]
	kind, ok := strings.CutSuffix(strings.ToLower(st.Type), "space")
	if !ok || !strings.EqualFold(st.Unit, "bytes") {
		return -1
	}
	return slices.IndexFunc(p.SampleTypes, func(t parser.SampleType) bool {
		return strings.EqualFold(t.Type, kind+"objects") && strings.EqualFold(t.Unit, "count")
	})
}

func isTimeUnit(unit string) bool {
	switch strings.ToLower(unit) {
	case "nanoseconds", "ns", "microseconds", "milliseconds", "seconds":
		return true
	}
	return false
}
