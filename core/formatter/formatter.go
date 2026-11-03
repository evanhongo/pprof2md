// Package formatter renders an analysis Result into Markdown. Two levels are
// available: summary (the executive summary alone: the top functions by self
// and by cumulative cost) and detail (the executive summary with a drill-down
// section, and the source, of each function it ranks).
package formatter

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"pprof2md/core/analyzer"
	"pprof2md/core/parser"
)

// Level selects the output format.
type Level string

const (
	LevelSummary Level = "summary"
	LevelDetail  Level = "detail"
)

// DefaultMaxCount is how many functions the executive summary ranks by self
// cost, and how many by cumulative cost, unless Options sets it.
const DefaultMaxCount = 5

// Options controls formatting. Zero values fall back to sensible defaults.
type Options struct {
	ProfileName string
	// SourceDir is where the detail level looks for the source files of the
	// profile; empty means the current directory. A file is read from its
	// own path when that is an absolute path that exists.
	SourceDir string
	// MaxSelfCount and MaxCumulativeCount are how many functions the
	// executive summary ranks by self cost and by cumulative cost. Values
	// <= 0 fall back to DefaultMaxCount.
	MaxSelfCount       int
	MaxCumulativeCount int
}

// Format renders the analysis at the requested level.
func Format(a *analyzer.Result, pt parser.ProfileType, level Level, opts Options) (string, error) {
	switch level {
	case LevelSummary:
		return Summary(a, pt, opts), nil
	case LevelDetail:
		return Detail(a, pt, opts), nil
	default:
		return "", fmt.Errorf("unknown format level: %s", level)
	}
}

// md is a tiny line-oriented Markdown accumulator.
type md struct {
	lines []string
}

func (m *md) p(s string)                 { m.lines = append(m.lines, s) }
func (m *md) pf(format string, a ...any) { m.lines = append(m.lines, fmt.Sprintf(format, a...)) }
func (m *md) blank()                     { m.lines = append(m.lines, "") }
func (m *md) string() string             { return strings.Join(m.lines, "\n") }

func profileName(opts Options) string {
	if opts.ProfileName == "" {
		return "profile.pb"
	}
	return opts.ProfileName
}

// costTerms holds the words that describe what a profile measures.
type costTerms struct {
	title     string // the report title, e.g. "CPU"
	self      string // the self cost, e.g. "self-time"
	selfLabel string // capitalized form for field labels, e.g. "Self-time"
	cum       string // the cumulative cost, e.g. "cumulative-time"
	cumLabel  string // capitalized form for field labels, e.g. "Cumulative-time"
	noun      string // what the percentages are a share of, e.g. "CPU"
	// measures says what a heap profile's sample type holds, e.g. "memory
	// allocated and not yet freed when the profile was taken". It is empty
	// for other profiles, and for sample types it does not know.
	measures string
}

// termsFor picks the words from the sample type that the analysis uses,
// and else from the profile type.
func termsFor(pt parser.ProfileType, st parser.SampleType) costTerms {
	title := strings.ToUpper(string(pt))
	switch {
	case pt == parser.ProfileCPU:
		return costTerms{title: title, self: "self-time", selfLabel: "Self-time", cum: "cumulative-time", cumLabel: "Cumulative-time", noun: "CPU"}
	case pt == parser.ProfileHeap:
		t := costTerms{title: title, self: "self-allocation", selfLabel: "Self-allocation", cum: "cumulative-allocation", cumLabel: "Cumulative-allocation", noun: "memory"}
		// Allocations include what was freed since; in-use memory does
		// not. Which one a heap profile holds decides how to read it.
		switch strings.ToLower(st.Type) {
		case "alloc_space":
			t.noun, t.measures = "allocated bytes", "all memory allocated during profiling, including memory freed since"
		case "alloc_objects":
			t.noun, t.measures = "allocated objects", "all objects allocated during profiling, including objects freed since"
		case "inuse_space", "space":
			t.noun, t.measures = "in-use bytes", "memory allocated and not yet freed when the profile was taken"
		case "inuse_objects", "objects":
			t.noun, t.measures = "in-use objects", "objects allocated and not yet freed when the profile was taken"
		}
		return t
	case pt == parser.ProfileMutex:
		return costTerms{title: title, self: "self-time", selfLabel: "Self-time", cum: "cumulative-time", cumLabel: "Cumulative-time", noun: "contention"}
	default:
		return costTerms{title: title, self: "self", selfLabel: "Self", cum: "cumulative", cumLabel: "Cumulative", noun: "the total"}
	}
}

// kindType is the "Type:" label for a function's kind of code.
func kindType(k analyzer.CodeKind) string {
	switch k {
	case analyzer.CodeNative:
		return "Native function"
	case analyzer.CodeLibrary:
		return "Runtime or library code"
	default:
		return "Application code"
	}
}

// maxCount is n, or DefaultMaxCount when n is not positive.
func maxCount(n int) int {
	if n <= 0 {
		return DefaultMaxCount
	}
	return n
}

// pct renders a percentage with one decimal place, e.g. "23.4%".
func pct(v float64) string { return fmt.Sprintf("%.1f%%", v) }

func itoa(n int) string { return strconv.Itoa(n) }

// humanInt renders an integer with thousands separators, e.g. 45231 -> 45,231.
func humanInt(n int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	digits := fmt.Sprintf("%d", n)
	var out strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(d)
	}
	if neg {
		return "-" + out.String()
	}
	return out.String()
}

// countNoun renders a count with a singular or plural noun, e.g. "1 sample",
// "1,200 samples".
func countNoun(n int, singular string) string {
	if n == 1 {
		return "1 " + singular
	}
	return humanInt(n) + " " + singular + "s"
}

// writeHeader renders the title and what the profile holds, shared by both
// levels. A heap profile says in words what its sample type measures, and
// the total amount; any other profile names its samples and sample type. A
// duration shows when the profile records one.
func writeHeader(m *md, a *analyzer.Result, pt parser.ProfileType, opts Options) {
	terms := termsFor(pt, a.SampleType)
	m.pf("# PPROF Analysis: %s", terms.title)
	m.blank()
	m.pf("**Profile:** `%s`", profileName(opts))

	var fields []string
	if a.DurationNanos > 0 {
		fields = append(fields, "**Duration:** "+formatDuration(a.DurationNanos))
	}
	if terms.measures != "" {
		m.pf("**Measures:** %s (`%s`): %s", terms.noun, a.SampleType.Type, terms.measures)
		// A diff's percentages are shares of its base, not of a total.
		if !a.Diff {
			fields = append(fields, "**Total:** "+amount(a, terms, a.TotalValue, a.TotalObjects, 0, " in "))
		}
	} else {
		fields = append(fields, "**Samples:** "+humanInt(a.TotalSamples))
		// A diff's total is its base, not what the profile sampled.
		if nanos, ok := timeNanos(a.TotalValue, a.SampleType.Unit); ok && !a.Diff {
			fields = append(fields, "**Total sample time:** "+formatSampleTime(nanos))
		}
		fields = append(fields, fmt.Sprintf("**Type:** %s (%s)", a.SampleType.Type, a.SampleType.Unit))
	}
	if len(fields) > 0 {
		m.p(strings.Join(fields, " | "))
	}
	m.blank()
}

// amount renders the cost behind a percentage. A heap profile whose sample
// type termsFor knows has no samples to count: its records are stacks, not
// samples. It shows the bytes, with the objects joined by sep when the
// profile counts them, or the objects when they are the value. Any other
// profile shows its samples.
func amount(a *analyzer.Result, terms costTerms, value, objects int64, samples int, sep string) string {
	if terms.measures == "" {
		return countNoun(samples, "sample")
	}
	if !strings.EqualFold(a.SampleType.Unit, "bytes") {
		return countNoun(int(value), "object")
	}
	s := formatBytes(value)
	if a.CountsObjects {
		s += sep + countNoun(int(objects), "object")
	}
	return s
}

// formatBytes renders a byte count with a binary unit, as pprof does: 1 KB is
// 1,024 bytes.
func formatBytes(n int64) string {
	if math.Abs(float64(n)) < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	v := float64(n)
	unit := ""
	for _, u := range []string{"KB", "MB", "GB", "TB", "PB"} {
		v /= 1024
		unit = u
		if math.Abs(v) < 1024 {
			break
		}
	}
	return fmt.Sprintf("%.1f %s", v, unit)
}

// timeNanos converts a value in a time unit to nanoseconds. It reports false
// when the unit is not a time.
func timeNanos(v int64, unit string) (int64, bool) {
	switch strings.ToLower(unit) {
	case "nanoseconds", "ns":
		return v, true
	case "microseconds":
		return v * 1e3, true
	case "milliseconds":
		return v * 1e6, true
	case "seconds":
		return v * 1e9, true
	}
	return 0, false
}

// formatSampleTime renders the time that samples add up to. Unlike
// formatDuration it keeps the resolution of a short time: "12.5ms".
func formatSampleTime(nanos int64) string {
	switch {
	case nanos >= 1e9:
		return formatDuration(nanos)
	case nanos >= 1e6:
		return fmt.Sprintf("%.1fms", float64(nanos)/1e6)
	case nanos >= 1e3:
		return fmt.Sprintf("%.1fµs", float64(nanos)/1e3)
	}
	return fmt.Sprintf("%dns", nanos)
}

func formatDuration(nanos int64) string {
	sec := float64(nanos) / 1e9
	if sec >= 60 {
		minutes := int(sec / 60)
		remain := math.Mod(sec, 60)
		return fmt.Sprintf("%dm %.0fs", minutes, remain)
	}
	return fmt.Sprintf("%.1fs", sec)
}

// profileFiles returns the source files of the profile's functions, with
// slashes.
func profileFiles(a *analyzer.Result) map[string]bool {
	files := make(map[string]bool)
	for _, s := range a.FunctionStats {
		if s.Filename != "" {
			files[slashed(s.Filename)] = true
		}
	}
	return files
}

// fileLocation names a source file and adds the line when it is known. For
// library code, what matters is which package or module it is: a Node
// built-in shows its module specifier (node:internal/crypto/hash), and a
// file in node_modules its path from the package name
// (express/lib/response.js, @scope/pkg/index.js; the innermost package when
// node_modules nest). Any other file shows its base name, with as many
// parent directories as it takes to tell it apart from the other files. It
// is empty for native code, which has no file.
func fileLocation(filename string, line int, files map[string]bool) string {
	if filename == "" {
		return ""
	}
	path := slashed(filename)
	var loc string
	if strings.HasPrefix(path, "node:") {
		loc = path
	} else if i := strings.LastIndex("/"+path, "/node_modules/"); i >= 0 {
		loc = path[i+len("node_modules/"):]
	} else {
		loc = distinctPath(path, files)
	}
	if line > 0 {
		loc += ":" + itoa(line)
	}
	return loc
}

// distinctPath returns the fewest trailing elements of path that no other
// file ends with, or the whole path.
func distinctPath(path string, files map[string]bool) string {
	parts := strings.Split(path, "/")
	for depth := 1; depth < len(parts); depth++ {
		suffix := strings.Join(parts[len(parts)-depth:], "/")
		shared := false
		for f := range files {
			if f != path && (f == suffix || strings.HasSuffix(f, "/"+suffix)) {
				shared = true
				break
			}
		}
		if !shared {
			return suffix
		}
	}
	return path
}

// slashed turns Windows path separators into slashes.
func slashed(filename string) string {
	return strings.ReplaceAll(filename, `\`, "/")
}
