// Package parser reads binary pprof profiles and normalizes them into a
// domain model that the rest of the core packages consume. It wraps the
// canonical github.com/google/pprof/profile decoder, which transparently
// handles gzip decompression, protobuf decoding and string-table resolution.
package parser

import (
	"fmt"
	"os"

	"github.com/google/pprof/profile"
)

// SampleType describes one measurement dimension of a profile (e.g. cpu /
// nanoseconds, or inuse_space / bytes).
type SampleType struct {
	Type string
	Unit string
}

// FunctionInfo is a resolved function record.
type FunctionInfo struct {
	ID         uint64
	Name       string
	SystemName string
	Filename   string
	StartLine  int
}

// LineInfo associates a source line with the function that owns it.
type LineInfo struct {
	FunctionID uint64
	Line       int
	Function   FunctionInfo
}

// Location is a program location: an address plus the (possibly inlined)
// source lines mapped to it.
type Location struct {
	ID      uint64
	Address uint64
	Lines   []LineInfo
}

// Label is a key/value annotation attached to a sample.
type Label struct {
	Key     string
	Str     string
	Num     int64
	NumUnit string
}

// Sample is a single stack sample with its per-sample-type values. The stack
// is ordered leaf-first: Stack[0] is the innermost frame.
type Sample struct {
	Stack  []Location
	Values []int64
	Labels []Label
}

// PeriodType describes the unit of the profiling period.
type PeriodType struct {
	Type string
	Unit string
}

// Mapping is a binary or shared library mapped into the profiled process.
// In Go profiles the first mapping is the main executable.
type Mapping struct {
	ID      uint64
	File    string
	BuildID string
}

// Profile is the normalized, fully-resolved representation of a pprof profile.
type Profile struct {
	SampleTypes       []SampleType
	Samples           []Sample
	Locations         map[uint64]Location
	Functions         map[uint64]FunctionInfo
	Mappings          []Mapping
	DropFrames        string
	KeepFrames        string
	Comment           []string
	DefaultSampleType string
	DurationNanos     int64
	TimeNanos         int64
	Period            int64
	PeriodType        *PeriodType
}

// ParseFile reads and parses a pprof profile from a file path.
func ParseFile(path string) (*Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read profile %q: %w", path, err)
	}
	return Parse(data)
}

// Parse parses a pprof profile from an in-memory buffer. The buffer may be a
// gzip-compressed encoded protobuf; decompression is handled automatically.
func Parse(data []byte) (*Profile, error) {
	p, err := profile.ParseData(data)
	if err != nil {
		return nil, fmt.Errorf("decode pprof: %w", err)
	}
	return normalize(p), nil
}

// normalize converts a decoded google/pprof profile into our domain model.
func normalize(p *profile.Profile) *Profile {
	sampleTypes := make([]SampleType, len(p.SampleType))
	for i, st := range p.SampleType {
		sampleTypes[i] = SampleType{Type: st.Type, Unit: st.Unit}
	}

	functions := make(map[uint64]FunctionInfo, len(p.Function))
	for _, fn := range p.Function {
		functions[fn.ID] = FunctionInfo{
			ID:         fn.ID,
			Name:       fn.Name,
			SystemName: fn.SystemName,
			Filename:   fn.Filename,
			StartLine:  int(fn.StartLine),
		}
	}

	mappings := make([]Mapping, len(p.Mapping))
	for i, m := range p.Mapping {
		mappings[i] = Mapping{ID: m.ID, File: m.File, BuildID: m.BuildID}
	}

	locations := make(map[uint64]Location, len(p.Location))
	for _, loc := range p.Location {
		locations[loc.ID] = newLocation(loc)
	}

	samples := make([]Sample, len(p.Sample))
	for i, s := range p.Sample {
		stack := make([]Location, len(s.Location))
		for j, loc := range s.Location {
			if resolved, ok := locations[loc.ID]; ok {
				stack[j] = resolved
			} else {
				stack[j] = newLocation(loc)
			}
		}

		values := make([]int64, len(s.Value))
		copy(values, s.Value)

		samples[i] = Sample{
			Stack:  stack,
			Values: values,
			Labels: flattenLabels(s),
		}
	}

	var periodType *PeriodType
	if p.PeriodType != nil {
		periodType = &PeriodType{Type: p.PeriodType.Type, Unit: p.PeriodType.Unit}
	}

	return &Profile{
		SampleTypes:       sampleTypes,
		Samples:           samples,
		Locations:         locations,
		Functions:         functions,
		Mappings:          mappings,
		DropFrames:        p.DropFrames,
		KeepFrames:        p.KeepFrames,
		Comment:           p.Comments,
		DefaultSampleType: p.DefaultSampleType,
		DurationNanos:     p.DurationNanos,
		TimeNanos:         p.TimeNanos,
		Period:            p.Period,
		PeriodType:        periodType,
	}
}

var unknownFunction = FunctionInfo{Name: "<unknown>"}

func newLocation(loc *profile.Location) Location {
	lines := make([]LineInfo, len(loc.Line))
	for i, ln := range loc.Line {
		fn := unknownFunction
		var fnID uint64
		if ln.Function != nil {
			fnID = ln.Function.ID
			fn = FunctionInfo{
				ID:         ln.Function.ID,
				Name:       ln.Function.Name,
				SystemName: ln.Function.SystemName,
				Filename:   ln.Function.Filename,
				StartLine:  int(ln.Function.StartLine),
			}
		}
		lines[i] = LineInfo{FunctionID: fnID, Line: int(ln.Line), Function: fn}
	}
	if len(lines) == 0 {
		// Mirror the TS fallback: a location with no resolvable lines still
		// contributes an <unknown> frame so stack arithmetic stays consistent.
		lines = []LineInfo{{Function: unknownFunction}}
	}
	return Location{ID: loc.ID, Address: loc.Address, Lines: lines}
}

// flattenLabels turns pprof's per-key label maps into a flat slice. Values are
// expected to have length 1 in practice.
func flattenLabels(s *profile.Sample) []Label {
	var labels []Label
	for key, vals := range s.Label {
		for _, v := range vals {
			labels = append(labels, Label{Key: key, Str: v})
		}
	}
	for key, nums := range s.NumLabel {
		units := s.NumUnit[key]
		for i, n := range nums {
			lbl := Label{Key: key, Num: n}
			if i < len(units) {
				lbl.NumUnit = units[i]
			}
			labels = append(labels, lbl)
		}
	}
	return labels
}
