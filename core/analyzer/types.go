// Package analyzer turns a normalized profile into aggregated statistics:
// per-function self/cumulative costs, a call tree, and the functions ranked
// by self and by cumulative cost. The heavy per-sample loops are parallelized
// with goroutines (map-reduce over sample chunks).
package analyzer

import "pprof2md/core/parser"

// FunctionStats holds the aggregated cost of a single function across all
// samples, keyed by its FuncRef key. Line is the function's hottest line: where
// it spends the most self cost, or its hottest call site if it has none.
// DeclLine is the line that identifies the function (FuncRef.Line, 0 when
// unknown), which labels use so a function reads the same everywhere.
type FunctionStats struct {
	Key             string
	Name            string
	Filename        string
	Line            int
	DeclLine        int
	Kind            CodeKind
	SelfValue       int64
	CumulativeValue int64
	// SelfPercent and CumulativePercent are shares of the total: the sum of
	// the sample values by magnitude, or the base profile's total for a diff
	// profile (whose percentages are signed).
	SelfPercent       float64
	CumulativePercent float64
	// SampleCount is the number of samples whose stack contains the
	// function, and SelfSampleCount the number where it is the leaf. A
	// profile that counts samples (samples/count in Go CPU, sample/count in
	// V8 time profiles) gives the samples each record stands for, by
	// magnitude; in any other profile a record is one sample.
	SampleCount     int
	SelfSampleCount int
	// Objects and SelfObjects are the objects allocated with the
	// function's cumulative and self values, from the object count that goes
	// with a heap profile's bytes (see Result.CountsObjects).
	Objects     int64
	SelfObjects int64

	lines lineCosts
}

// Hotspot is a function that has a self or cumulative cost, with the
// FunctionStats fields that describe it.
type Hotspot struct {
	Key               string
	Name              string
	Filename          string
	Line              int
	DeclLine          int
	Kind              CodeKind
	SelfPercent       float64
	CumulativePercent float64
	SampleCount       int
	SelfSampleCount   int
	SelfValue         int64
	CumulativeValue   int64
	Objects           int64
	SelfObjects       int64
}

// CallTreeNode is a node in the aggregated call tree. Children are keyed by
// FuncRef key. CumulativeValue sums the values of the samples through the
// node, so gains and losses below it cancel out in a diff profile.
type CallTreeNode struct {
	Key             string
	Name            string
	Filename        string
	DeclLine        int
	CumulativeValue int64
	Children        map[string]*CallTreeNode
}

// CodeKind classifies a function by who can change it.
type CodeKind string

const (
	// CodeNative has no source file: VM built-ins such as JSON.parse,
	// native code, GC.
	CodeNative CodeKind = "native"
	// CodeLibrary is a language runtime, standard library or dependency.
	CodeLibrary CodeKind = "library"
	// CodeApplication is the profiled program's own code.
	CodeApplication CodeKind = "application"
)

// FuncRef identifies a function: Key tells functions apart (see funcRef),
// and Filename and Line locate it.
type FuncRef struct {
	Key      string
	Name     string
	Filename string
	Line     int
}

// Result is the full analysis of a profile for one sample type.
type Result struct {
	SampleType parser.SampleType
	// TotalSamples counts the samples with a value for SampleType: the sum
	// of the profile's sample counts (see FunctionStats.SampleCount).
	TotalSamples int
	// TotalValue is the total that the percentages are shares of: the sum
	// of the values by magnitude, or the base total of a diff profile.
	TotalValue int64
	// CountsObjects is set when SampleType is a heap profile's bytes that
	// come with an object count of the same kind: alloc_objects with
	// alloc_space, inuse_objects with inuse_space, objects with V8's space.
	// TotalObjects sums that count by magnitude, like TotalSamples.
	CountsObjects bool
	TotalObjects  int64
	FunctionStats map[string]*FunctionStats
	CallTree      *CallTreeNode
	// Hotspots are the functions ranked by self cost, and CumulativeHotspots
	// those ranked by cumulative cost.
	Hotspots           []Hotspot
	CumulativeHotspots []Hotspot
	// Diff is set when the profile has negative values, as a diff profile
	// does: percentages are then signed changes.
	Diff          bool
	DurationNanos int64
}

// Options configures Analyze.
type Options struct {
	// SampleTypeIndex selects which value dimension to analyze (default 0).
	SampleTypeIndex int
}

func newNode(name string) *CallTreeNode {
	return &CallTreeNode{Key: name, Name: name, Children: map[string]*CallTreeNode{}}
}
