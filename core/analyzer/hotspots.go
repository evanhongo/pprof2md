package analyzer

import (
	"cmp"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
)

// identifyHotspots selects functions with a self cost, sorted by self-percent
// descending (ties broken by key for determinism). The sort compares by
// magnitude, so a diff profile ranks regressions and improvements together.
func identifyHotspots(stats map[string]*FunctionStats) []Hotspot {
	hotspots := make([]Hotspot, 0)
	for _, s := range stats {
		if s.SelfValue != 0 {
			hotspots = append(hotspots, newHotspot(s))
		}
	}

	slices.SortFunc(hotspots, func(a, b Hotspot) int {
		return cmp.Or(
			cmp.Compare(math.Abs(b.SelfPercent), math.Abs(a.SelfPercent)),
			cmp.Compare(a.Key, b.Key),
		)
	})

	return hotspots
}

// identifyCumulativeHotspots selects functions with a cumulative cost, sorted
// by cumulative percent descending. Among equal ones, the function with more
// self cost comes first: it does the work that the others only pass through.
// Remaining ties are broken by key. All compare by magnitude, as in
// identifyHotspots.
func identifyCumulativeHotspots(stats map[string]*FunctionStats) []Hotspot {
	hotspots := make([]Hotspot, 0)
	for _, s := range stats {
		if s.CumulativeValue != 0 {
			hotspots = append(hotspots, newHotspot(s))
		}
	}

	slices.SortFunc(hotspots, func(a, b Hotspot) int {
		return cmp.Or(
			cmp.Compare(math.Abs(b.CumulativePercent), math.Abs(a.CumulativePercent)),
			cmp.Compare(math.Abs(b.SelfPercent), math.Abs(a.SelfPercent)),
			cmp.Compare(a.Key, b.Key),
		)
	})

	return hotspots
}

// newHotspot makes the hotspot of a function from its statistics.
func newHotspot(s *FunctionStats) Hotspot {
	return Hotspot{
		Key:               s.Key,
		Name:              s.Name,
		Filename:          s.Filename,
		Line:              s.Line,
		DeclLine:          s.DeclLine,
		Kind:              s.Kind,
		SelfPercent:       s.SelfPercent,
		CumulativePercent: s.CumulativePercent,
		SampleCount:       s.SampleCount,
		SelfSampleCount:   s.SelfSampleCount,
		SelfValue:         s.SelfValue,
		CumulativeValue:   s.CumulativeValue,
		Objects:           s.Objects,
		SelfObjects:       s.SelfObjects,
	}
}

// Labels returns a display label for each ref, in order: the function's name,
// followed by its location when another ref with the same name is a
// different function. The location is the file's base name and line, with as
// many parent directories as it takes to tell the functions apart. Repeats of
// the same function (recursion) keep the plain name.
func Labels(refs []FuncRef) []string {
	byName := make(map[string][]FuncRef)
	for _, r := range refs {
		if !slices.ContainsFunc(byName[r.Name], func(o FuncRef) bool { return o.Key == r.Key }) {
			byName[r.Name] = append(byName[r.Name], r)
		}
	}
	locations := make(map[string]string) // by key
	for _, group := range byName {
		if len(group) > 1 {
			maps.Copy(locations, distinctLocations(group))
		}
	}
	labels := make([]string, len(refs))
	for i, r := range refs {
		labels[i] = r.Name
		if loc := locations[r.Key]; loc != "" {
			labels[i] += " (" + loc + ")"
		}
	}
	return labels
}

// distinctLocations returns "dir/file:line" locations for same-named
// functions, keyed by function key, using the fewest trailing path elements
// that tell them apart. A function whose line is unknown (0) gets just
// "dir/file", and a native function (no file) gets none.
func distinctLocations(group []FuncRef) map[string]string {
	maxDepth := 1
	for _, r := range group {
		maxDepth = max(maxDepth, strings.Count(slashed(r.Filename), "/")+1)
	}
	locs := make(map[string]string, len(group))
	for depth := 1; depth <= maxDepth; depth++ {
		clear(locs)
		seen := make(map[string]bool, len(group))
		distinct := true
		for _, r := range group {
			if r.Filename == "" {
				continue
			}
			parts := strings.Split(slashed(r.Filename), "/")
			loc := strings.Join(parts[max(0, len(parts)-depth):], "/")
			if r.Line != 0 {
				loc = fmt.Sprintf("%s:%d", loc, r.Line)
			}
			distinct = distinct && !seen[loc]
			seen[loc] = true
			locs[r.Key] = loc
		}
		if distinct {
			break
		}
	}
	return locs
}
