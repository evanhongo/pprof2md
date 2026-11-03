package formatter

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"pprof2md/core/analyzer"
	"pprof2md/core/parser"
	"pprof2md/core/source"
)

// Detail renders the executive summary with a drill-down section, and the
// source, of each function it ranks. Source snippets are resolved
// concurrently up front.
func Detail(a *analyzer.Result, pt parser.ProfileType, opts Options) string {
	summary := newExecutiveSummary(a, opts)

	// Native code has no source to show.
	reqs := make([]source.Request, 0, len(summary.funcs))
	for _, h := range summary.funcs {
		if h.Kind != analyzer.CodeNative && h.Filename != "" {
			reqs = append(reqs, source.Request{Filename: h.Filename, Line: h.Line})
		}
	}
	sources := source.ResolveMultiple(reqs, opts.SourceDir, source.DefaultContextLines)

	m := &md{}
	writeHeader(m, a, pt, opts)
	summary.write(m, a, pt, true)
	if len(summary.funcs) == 0 {
		return m.string()
	}
	m.blank()

	m.p("---")
	m.blank()
	m.p("## Detailed Analysis")
	m.blank()

	files := profileFiles(a)
	for _, h := range summary.funcs {
		m.pf(`<a id="%s"></a>`, summary.ids[h.Key])
		m.blank()
		m.p(formatDrillDownSection(h, summary.labels[h.Key], fileLocation(h.Filename, h.Line, files), a, pt, sources))
		m.blank()
	}

	return m.string()
}

var drillIDNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func generateDrillID(name string) string {
	id := drillIDNonAlnum.ReplaceAllString(strings.ToLower(name), "-")
	id = strings.Trim(id, "-")
	if len(id) > 30 {
		id = id[:30]
	}
	return id
}

// hotspotLabels names each hotspot, adding its location when another
// hotspot shares the name.
func hotspotLabels(hotspots []analyzer.Hotspot) []string {
	refs := make([]analyzer.FuncRef, len(hotspots))
	for i, h := range hotspots {
		refs[i] = analyzer.FuncRef{Key: h.Key, Name: h.Name, Filename: h.Filename, Line: h.DeclLine}
	}
	return analyzer.Labels(refs)
}

// drillIDs turns labels into anchor ids, numbering repeats (e.g. from
// truncation) so every anchor is unique.
func drillIDs(labels []string) []string {
	ids := make([]string, len(labels))
	used := make(map[string]bool, len(labels))
	for i, l := range labels {
		base := generateDrillID(l)
		id := base
		for n := 2; used[id]; n++ {
			id = base + "-" + itoa(n)
		}
		used[id] = true
		ids[i] = id
	}
	return ids
}

// formatDrillDownSection renders a function's section. loc is where the
// function is (see fileLocation), empty for native code.
func formatDrillDownSection(h analyzer.Hotspot, label, loc string, a *analyzer.Result, pt parser.ProfileType, sources map[string]*source.Context) string {
	terms := termsFor(pt, a.SampleType)

	m := &md{}
	m.pf("### `%s`", label)
	m.blank()

	if loc != "" {
		m.pf("**Location:** `%s`", loc)
	}
	if path := findPathToFunction(a.CallTree, h.Key); len(path) > 0 {
		m.pf("**Call path:** %s", chain(path, a))
	}

	m.pf("**%s:** %s (%s)", terms.selfLabel, pct(h.SelfPercent), amount(a, terms, h.SelfValue, h.SelfObjects, h.SelfSampleCount, ", "))
	m.pf("**%s:** %s (%s)", terms.cumLabel, pct(h.CumulativePercent), amount(a, terms, h.CumulativeValue, h.Objects, h.SampleCount, ", "))
	m.pf("**Type:** %s", kindType(h.Kind))

	if src := sources[fmt.Sprintf("%s:%d", h.Filename, h.Line)]; src != nil {
		m.blank()
		m.p("**Source:**")
		m.blank()
		m.p("```")
		m.pf("// %s:%d-%d", src.Filename, src.StartLine, src.EndLine)
		for i, line := range src.Lines {
			lineNum := src.StartLine + i
			marker := ""
			if lineNum == h.Line {
				marker = " // ← HOT"
			}
			m.p(line + marker)
		}
		m.p("```")
	}

	return m.string()
}

// findPathToFunction returns the chain from the root to the call-tree node of
// the function with the given key that carries the most cumulative cost
// (by magnitude, for diff profiles).
func findPathToFunction(tree *analyzer.CallTreeNode, key string) []analyzer.FuncRef {
	var best []analyzer.FuncRef
	var bestCum int64 = -1
	var walk func(node *analyzer.CallTreeNode, path []analyzer.FuncRef)
	walk = func(node *analyzer.CallTreeNode, path []analyzer.FuncRef) {
		path = append(path, analyzer.FuncRef{Key: node.Key, Name: node.Name, Filename: node.Filename, Line: node.DeclLine})
		if cum := abs(node.CumulativeValue); node.Key == key && (cum > bestCum || (cum == bestCum && pathLess(path, best))) {
			best, bestCum = slices.Clone(path), cum
		}
		for _, child := range node.Children {
			walk(child, path)
		}
	}
	walk(tree, nil)
	return best
}

// pathLess orders equally hot paths by their keys, so the choice does not
// depend on map iteration order.
func pathLess(a, b []analyzer.FuncRef) bool {
	return slices.CompareFunc(a, b, func(x, y analyzer.FuncRef) int { return strings.Compare(x.Key, y.Key) }) < 0
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// minCollapsedFrames is the fewest frames that collapsing a run of library
// frames hides: hiding fewer saves next to nothing.
const minCollapsedFrames = 3

// chain renders a call chain as `a` → `b` → `c`. In a run of library frames
// (frames that are not application code: runtime, library and native code),
// the frames between the first and the last collapse into "… N library
// frames …" when there are at least minCollapsedFrames of them. The frames
// that stay show where the chain starts and ends, and where it enters and
// leaves application code. Labels tell apart only the frames shown. A frame
// with no function (the call tree's root) stays and ends a run.
func chain(refs []analyzer.FuncRef, a *analyzer.Result) string {
	isLibrary := func(r analyzer.FuncRef) bool {
		s := a.FunctionStats[r.Key]
		return s != nil && s.Kind != analyzer.CodeApplication
	}
	var (
		shown  []analyzer.FuncRef
		hidden []int // per shown frame: the frames collapsed after it
	)
	show := func(rs ...analyzer.FuncRef) {
		shown = append(shown, rs...)
		hidden = append(hidden, make([]int, len(rs))...)
	}
	for i := 0; i < len(refs); {
		j := i // refs[i:j] is a run of library frames
		for j < len(refs) && isLibrary(refs[j]) {
			j++
		}
		if n := j - i - 2; n >= minCollapsedFrames {
			show(refs[i])
			hidden[len(hidden)-1] = n
			show(refs[j-1])
		} else {
			show(refs[i:j]...)
		}
		if j < len(refs) {
			show(refs[j])
			j++
		}
		i = j
	}

	var b strings.Builder
	for i, l := range analyzer.Labels(shown) {
		if i > 0 {
			b.WriteString(" → ")
		}
		b.WriteString("`" + l + "`")
		if hidden[i] > 0 {
			b.WriteString(" → … " + countNoun(hidden[i], "library frame") + " …")
		}
	}
	return b.String()
}
