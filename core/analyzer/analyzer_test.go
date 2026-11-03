package analyzer_test

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pprof2md/core/analyzer"
	"pprof2md/core/parser"
)

const profilesDir = "../../test/profiles"

func loadProfile(t *testing.T, name string) *parser.Profile {
	t.Helper()
	p := filepath.Join(profilesDir, name)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("fixture %s not found; run `npm run generate-profiles` in the reference repo", name)
	}
	prof, err := parser.ParseFile(p)
	if err != nil {
		t.Fatalf("ParseFile(%s): %v", name, err)
	}
	return prof
}

func analyzeCPU(t *testing.T) *analyzer.Result {
	t.Helper()
	p := loadProfile(t, "cpu-test.pb.gz")
	idx := analyzer.GetPrimarySampleTypeIndex(p, parser.ProfileCPU)
	res, err := analyzer.Analyze(p, analyzer.Options{SampleTypeIndex: idx})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	return res
}

func TestAnalyzeProducesValidResult(t *testing.T) {
	res := analyzeCPU(t)

	if res.SampleType.Type == "" {
		t.Error("should have a sample type")
	}
	if res.TotalSamples <= 0 {
		t.Error("should have samples")
	}
	if res.FunctionStats == nil {
		t.Error("should have function stats")
	}
	if res.CallTree == nil {
		t.Error("should have a call tree")
	}
	if res.Hotspots == nil {
		t.Error("should have a hotspots slice")
	}
	if res.CumulativeHotspots == nil {
		t.Error("should have a cumulative hotspots slice")
	}
}

func TestHotspotsSortedBySelfPercentDesc(t *testing.T) {
	res := analyzeCPU(t)
	if len(res.Hotspots) < 2 {
		t.Skip("not enough hotspots to check ordering")
	}
	for i := 1; i < len(res.Hotspots); i++ {
		if res.Hotspots[i-1].SelfPercent < res.Hotspots[i].SelfPercent {
			t.Errorf("hotspots not sorted: index %d (%.4f) < index %d (%.4f)",
				i-1, res.Hotspots[i-1].SelfPercent, i, res.Hotspots[i].SelfPercent)
		}
	}
}

func cumulativeRanking(res *analyzer.Result) string {
	var ranked []string
	for _, h := range res.CumulativeHotspots {
		ranked = append(ranked, fmt.Sprintf("%s=%.1f", h.Name, h.CumulativePercent))
	}
	return strings.Join(ranked, ",")
}

// Among functions with the same cumulative cost, the one that does the work
// itself comes first.
func TestCumulativeHotspotsRankByCumulativeThenSelf(t *testing.T) {
	res, err := analyzer.Analyze(branchingProfile(), analyzer.Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	want := "processRequest=100.0,handleHTTP=100.0,JSON.parse=60.0,parseBody=60.0,RegExp.exec=30.0,validateEmail=30.0"
	if got := cumulativeRanking(res); got != want {
		t.Errorf("cumulative hotspots = [%s], want [%s]", got, want)
	}
}

func selfRanking(res *analyzer.Result) string {
	var ranked []string
	for _, h := range res.Hotspots {
		ranked = append(ranked, fmt.Sprintf("%s=%.1f", h.Name, h.SelfPercent))
	}
	return strings.Join(ranked, ",")
}

// Every function with a cost ranks, however small the cost: a function
// without self cost (main) is left out of the self ranking only.
func TestHotspotsKeepEveryFunctionWithCost(t *testing.T) {
	m := frame("main", "m.js", 1)
	p := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{frame("big", "b.js", 1), m}, Values: []int64{199}},
			{Stack: []parser.Location{frame("tiny", "t.js", 1), m}, Values: []int64{1}},
		},
	}
	res, err := analyzer.Analyze(p, analyzer.Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if got, want := selfRanking(res), "big=99.5,tiny=0.5"; got != want {
		t.Errorf("hotspots = [%s], want [%s]", got, want)
	}
	if got, want := cumulativeRanking(res), "main=100.0,big=99.5,tiny=0.5"; got != want {
		t.Errorf("cumulative hotspots = [%s], want [%s]", got, want)
	}
}

func TestCumulativeHotspotsOfDiffRankBySize(t *testing.T) {
	p := diffProfile()
	m := goFrame("main", "main.go", 1, 2)
	for i := range p.Samples {
		p.Samples[i].Stack = append(p.Samples[i].Stack, m)
	}
	res, err := analyzer.Analyze(p, analyzer.Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	// main's gains and losses cancel out.
	if got, want := cumulativeRanking(res), "A=25.0,B=-25.0"; got != want {
		t.Errorf("cumulative hotspots = [%s], want [%s]", got, want)
	}
}

func TestSelfPercentSumWithinBounds(t *testing.T) {
	res := analyzeCPU(t)
	var sum float64
	for _, s := range res.FunctionStats {
		sum += s.SelfPercent
	}
	// Self percentages partition the total, so they must not exceed 100
	// (small rounding slack allowed).
	if sum > 100.1 {
		t.Errorf("self-percent sum = %.4f, want <= 100.1", sum)
	}
}

func TestGetPrimarySampleTypeIndex(t *testing.T) {
	p := loadProfile(t, "cpu-test.pb.gz")
	idx := analyzer.GetPrimarySampleTypeIndex(p, parser.ProfileCPU)
	if idx < 0 || idx >= len(p.SampleTypes) {
		t.Errorf("primary sample type index %d out of range [0,%d)", idx, len(p.SampleTypes))
	}
}

func frame(name, filename string, line int) parser.Location {
	return parser.Location{Lines: []parser.LineInfo{{
		Line:     line,
		Function: parser.FunctionInfo{Name: name, Filename: filename},
	}}}
}

// branchingProfile: handleHTTP → processRequest fans out into a 60% branch
// (parseBody → JSON.parse) and a 30% branch (validateEmail → RegExp.exec).
func branchingProfile() *parser.Profile {
	pr, hh := frame("processRequest", "handler.ts", 88), frame("handleHTTP", "handler.ts", 25)
	return &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{frame("JSON.parse", "", 0), frame("parseBody", "handler.ts", 142), pr, hh}, Values: []int64{300}},
			{Stack: []parser.Location{frame("RegExp.exec", "", 0), frame("validateEmail", "handler.ts", 60), pr, hh}, Values: []int64{150}},
			{Stack: []parser.Location{pr, hh}, Values: []int64{50}},
		},
	}
}

func TestProfileWithoutNegativeValuesIsNotDiff(t *testing.T) {
	res, err := analyzer.Analyze(branchingProfile(), analyzer.Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if res.Diff {
		t.Error("Diff = true for a profile without negative values")
	}
}

// inlined builds a location whose lines are ordered innermost-first, as in
// pprof: lines[0] is the inlined callee, the last line is the outer caller.
func inlined(lines ...parser.LineInfo) parser.Location {
	return parser.Location{Lines: lines}
}

func line(name, filename string, n int) parser.LineInfo {
	return parser.LineInfo{Line: n, Function: parser.FunctionInfo{Name: name, Filename: filename}}
}

// inlineProfile: main calls outer, which has inner inlined into it, so the
// leaf location carries two lines.
func inlineProfile() *parser.Profile {
	return &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{{
			Stack: []parser.Location{
				inlined(line("inner", "a.go", 10), line("outer", "a.go", 20)),
				frame("main", "a.go", 30),
			},
			Values: []int64{100},
		}},
	}
}

func statByName(t *testing.T, res *analyzer.Result, name string) *analyzer.FunctionStats {
	t.Helper()
	for _, s := range res.FunctionStats {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no function stats for %s", name)
	return nil
}

func TestInlinedFramesSelfOnlyOnInnermost(t *testing.T) {
	res, err := analyzer.Analyze(inlineProfile(), analyzer.Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	want := map[string]struct{ self, cum int64 }{
		"inner": {100, 100},
		"outer": {0, 100},
		"main":  {0, 100},
	}
	for name, w := range want {
		s := statByName(t, res, name)
		if s.SelfValue != w.self || s.CumulativeValue != w.cum {
			t.Errorf("%s self/cum = %d/%d, want %d/%d", name, s.SelfValue, s.CumulativeValue, w.self, w.cum)
		}
	}
}

func TestInlinedFramesCallTreeOrder(t *testing.T) {
	res, err := analyzer.Analyze(inlineProfile(), analyzer.Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	var names []string
	node := res.CallTree
	for len(node.Children) == 1 {
		for _, c := range node.Children {
			node = c
		}
		names = append(names, node.Name)
		if node.CumulativeValue != 100 {
			t.Errorf("%s cum = %d, want 100", node.Name, node.CumulativeValue)
		}
	}
	if got, want := strings.Join(names, " → "), "main → outer → inner"; got != want {
		t.Errorf("call tree = %s, want %s", got, want)
	}
}

// goFrame builds a Go-style frame: StartLine identifies the function, and
// line is the sampled line inside it.
func goFrame(name, filename string, startLine, line int) parser.Location {
	return parser.Location{Lines: []parser.LineInfo{{
		Line:     line,
		Function: parser.FunctionInfo{Name: name, Filename: filename, StartLine: startLine},
	}}}
}

func singleStat(t *testing.T, res *analyzer.Result, name string) *analyzer.FunctionStats {
	t.Helper()
	var found *analyzer.FunctionStats
	for _, s := range res.FunctionStats {
		if s.Name == name {
			if found != nil {
				t.Fatalf("%s split into several stats: %s and %s", name, found.Key, s.Key)
			}
			found = s
		}
	}
	if found == nil {
		t.Fatalf("no function stats for %s", name)
	}
	return found
}

// goLinesProfile: F (starting at line 1) calls G from line 5 and H from
// line 6, and spends its own time on line 7.
func goLinesProfile() *parser.Profile {
	return &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{goFrame("G", "f.go", 20, 21), goFrame("F", "f.go", 1, 5)}, Values: []int64{45}},
			{Stack: []parser.Location{goFrame("H", "f.go", 30, 31), goFrame("F", "f.go", 1, 6)}, Values: []int64{45}},
			{Stack: []parser.Location{goFrame("F", "f.go", 1, 7)}, Values: []int64{10}},
		},
	}
}

func TestGoFunctionAggregatesAcrossLines(t *testing.T) {
	res, err := analyzer.Analyze(goLinesProfile(), analyzer.Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	f := singleStat(t, res, "F")
	if f.CumulativeValue != 100 || f.SelfValue != 10 {
		t.Errorf("F self/cum = %d/%d, want 10/100", f.SelfValue, f.CumulativeValue)
	}
	// The line shown is where the function spends its own time.
	if f.Line != 7 {
		t.Errorf("F line = %d, want 7", f.Line)
	}
	// A caller-only function shows its hottest call site.
	if g := singleStat(t, res, "G"); g.Line != 21 {
		t.Errorf("G line = %d, want 21", g.Line)
	}
}

func TestGoSelfSpreadAcrossLinesIsOneHotspot(t *testing.T) {
	p := &parser.Profile{SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}}}
	p.Samples = append(p.Samples, parser.Sample{Stack: []parser.Location{goFrame("main", "m.go", 1, 2)}, Values: []int64{950}})
	for i := range 10 {
		p.Samples = append(p.Samples, parser.Sample{Stack: []parser.Location{goFrame("F", "f.go", 1, 10+i)}, Values: []int64{5}})
	}
	res, err := analyzer.Analyze(p, analyzer.Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	var hot []string
	for _, h := range res.Hotspots {
		hot = append(hot, h.Name)
	}
	if got := strings.Join(hot, ","); got != "main,F" {
		t.Errorf("hotspots = [%s], want [main,F]", got)
	}
}

func TestGoCallTreeMergesLinesOfAFunction(t *testing.T) {
	res, err := analyzer.Analyze(goLinesProfile(), analyzer.Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if len(res.CallTree.Children) != 1 {
		t.Fatalf("root has %d children, want 1", len(res.CallTree.Children))
	}
	for _, f := range res.CallTree.Children {
		if f.Name != "F" || f.CumulativeValue != 100 {
			t.Errorf("F node = %s cum %d, want F 100", f.Name, f.CumulativeValue)
		}
		if len(f.Children) != 2 {
			t.Errorf("F has %d children, want 2 (G, H)", len(f.Children))
		}
	}
}

// A profile without start lines does not tell same-named functions in one
// file apart: V8 exporters such as @datadog/pprof report each function's own
// line, but the key has no line, so two getters make one function. Each
// line is tallied, so the hottest one shows.
func TestNoStartLineMergesSameNamedFunctionsInAFile(t *testing.T) {
	get := func(line int) parser.Location {
		return parser.Location{Lines: []parser.LineInfo{{
			FunctionID: 7,
			Line:       line,
			Function:   parser.FunctionInfo{ID: 7, Name: "get", Filename: "readable.js"},
		}}}
	}
	p := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{get(244)}, Values: []int64{40}},
			{Stack: []parser.Location{get(147)}, Values: []int64{60}},
			{Stack: []parser.Location{frame("(anonymous)", "", 3)}, Values: []int64{10}},
			{Stack: []parser.Location{frame("(anonymous)", "", 9)}, Values: []int64{10}},
		},
	}
	res, err := analyzer.Analyze(p, analyzer.Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(res.FunctionStats) != 2 {
		t.Errorf("got %d function stats, want 2", len(res.FunctionStats))
	}
	if g := singleStat(t, res, "get"); g.SelfValue != 100 || g.Line != 147 {
		t.Errorf("get self = %d line %d, want 100 line 147", g.SelfValue, g.Line)
	}
}

// Profilers that leave StartLine at 0 and report the sampled line (Go before
// 1.20, some Rust and Java exporters) make a function span several lines,
// and it is still one function.
func TestNoStartLineAggregatesAcrossLines(t *testing.T) {
	p := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{frame("hot", "/app/hot.rs", 11), frame("main", "/app/main.rs", 5)}, Values: []int64{50}},
			{Stack: []parser.Location{frame("hot", "/app/hot.rs", 10), frame("main", "/app/main.rs", 6)}, Values: []int64{50}},
		},
	}
	res, err := analyzer.Analyze(p, analyzer.Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	hot := singleStat(t, res, "hot")
	if hot.SelfPercent != 100 || hot.Line != 10 {
		t.Errorf("hot self = %.0f%% line %d, want 100%% line 10", hot.SelfPercent, hot.Line)
	}
	if m := singleStat(t, res, "main"); m.CumulativePercent != 100 {
		t.Errorf("main cum = %.0f%%, want 100%%", m.CumulativePercent)
	}
	// One call-tree node, too.
	if m := childNamed(t, res.CallTree, "main"); len(m.Children) != 1 || childNamed(t, m, "hot").CumulativeValue != 100 {
		t.Errorf("main has %d children, want one hot with 100", len(m.Children))
	}
}

// Such a function has no line of its own, so a label shows only its file.
func TestLabelsOmitAnUnknownLine(t *testing.T) {
	refs := []analyzer.FuncRef{
		{Key: "a", Name: "hot", Filename: "/app/a/hot.rs"},
		{Key: "b", Name: "hot", Filename: "/app/b/hot.rs"},
	}
	if got, want := strings.Join(analyzer.Labels(refs), " | "), "hot (a/hot.rs) | hot (b/hot.rs)"; got != want {
		t.Errorf("Labels = %s, want %s", got, want)
	}
}

// diffProfile mimics `go tool pprof -proto -diff_base`: base samples are
// negated and labeled pprof::base. Base: A=100, B=100. New: A=150, B=50.
func diffProfile() *parser.Profile {
	base := []parser.Label{{Key: "pprof::base", Str: "true"}}
	a, b := goFrame("A", "a.go", 1, 2), goFrame("B", "b.go", 1, 2)
	return &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{a}, Values: []int64{-100}, Labels: base},
			{Stack: []parser.Location{b}, Values: []int64{-100}, Labels: base},
			{Stack: []parser.Location{a}, Values: []int64{150}},
			{Stack: []parser.Location{b}, Values: []int64{50}},
		},
	}
}

func TestDiffProfilePercentsUseBaseTotal(t *testing.T) {
	res, err := analyzer.Analyze(diffProfile(), analyzer.Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if !res.Diff {
		t.Error("Diff = false, want true")
	}
	// A changed by +50 and B by -50, of a base total of 200.
	var hot []string
	for _, h := range res.Hotspots {
		hot = append(hot, fmt.Sprintf("%s=%.0f", h.Name, h.SelfPercent))
	}
	if got, want := strings.Join(hot, ","), "A=25,B=-25"; got != want {
		t.Errorf("hotspots = [%s], want [%s]", got, want)
	}
}

func TestNegativeValuesWithoutBaseLabelStayBounded(t *testing.T) {
	p := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{frame("A", "a.js", 1)}, Values: []int64{1000}},
			{Stack: []parser.Location{frame("B", "b.js", 1)}, Values: []int64{-999}},
		},
	}
	res, err := analyzer.Analyze(p, analyzer.Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	for _, s := range res.FunctionStats {
		if math.Abs(s.SelfPercent) > 100 {
			t.Errorf("%s self = %.2f%%, want within ±100%%", s.Name, s.SelfPercent)
		}
	}
}

func TestNetZeroDiffKeepsData(t *testing.T) {
	p := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{frame("A", "a.js", 1)}, Values: []int64{50}},
			{Stack: []parser.Location{frame("B", "b.js", 1)}, Values: []int64{-50}},
		},
	}
	res, err := analyzer.Analyze(p, analyzer.Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(res.Hotspots) != 2 {
		t.Errorf("got %d hotspots, want 2", len(res.Hotspots))
	}
}

func TestCodeKindOfNodeBuiltinsAndDependencies(t *testing.T) {
	p := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{frame("readFileSync", "node:fs", 400), frame("processTimers", "node:internal/timers", 600)}, Values: []int64{50}},
			{Stack: []parser.Location{frame("handle", "/app/node_modules/express/lib/router/layer.js", 86)}, Values: []int64{30}},
			{Stack: []parser.Location{frame("(garbage collector)", "", 0)}, Values: []int64{20}},
			// Native code stays native, even when application code calls it.
			{Stack: []parser.Location{frame("JSON.parse", "", 0), frame("handler", "/app/src/handler.js", 3), frame("emit", "node:events", 489)}, Values: []int64{100}},
		},
	}
	res, err := analyzer.Analyze(p, analyzer.Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	want := map[string]analyzer.CodeKind{
		"readFileSync":        analyzer.CodeLibrary,
		"processTimers":       analyzer.CodeLibrary,
		"handle":              analyzer.CodeLibrary,
		"emit":                analyzer.CodeLibrary,
		"(garbage collector)": analyzer.CodeNative,
		"JSON.parse":          analyzer.CodeNative,
		"handler":             analyzer.CodeApplication,
	}
	for name, kind := range want {
		if got := statByName(t, res, name).Kind; got != kind {
			t.Errorf("%s kind = %s, want %s", name, got, kind)
		}
	}
}

func TestCodeKindOfGoRuntimeAndModules(t *testing.T) {
	goroot := "/usr/local/go/src/"
	p := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{
				goFrame("runtime.mallocgc", goroot+"runtime/malloc.go", 900, 1000),
				goFrame("main.work", "/app/main.go", 10, 12),
				goFrame("runtime.main", goroot+"runtime/proc.go", 140, 280),
			}, Values: []int64{60}},
			{Stack: []parser.Location{goFrame("runtime.gcBgMarkWorker", goroot+"runtime/mgc.go", 1300, 1400)}, Values: []int64{30}},
			{Stack: []parser.Location{
				goFrame("github.com/x/y.(*T).F", "/root/go/pkg/mod/github.com/x/y@v1.0.0/y.go", 5, 6),
				goFrame("runtime.goexit", goroot+"runtime/asm_amd64.s", 1700, 1700),
			}, Values: []int64{10}},
			// A module path without a domain is still application code.
			{Stack: []parser.Location{goFrame("myapp/internal/db.Query", "/srv/myapp/internal/db/q.go", 20, 25)}, Values: []int64{20}},
		},
	}
	res, err := analyzer.Analyze(p, analyzer.Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	want := map[string]analyzer.CodeKind{
		"runtime.mallocgc":        analyzer.CodeLibrary,
		"runtime.main":            analyzer.CodeLibrary,
		"runtime.gcBgMarkWorker":  analyzer.CodeLibrary,
		"runtime.goexit":          analyzer.CodeLibrary,
		"github.com/x/y.(*T).F":   analyzer.CodeLibrary,
		"main.work":               analyzer.CodeApplication,
		"myapp/internal/db.Query": analyzer.CodeApplication,
	}
	for name, kind := range want {
		if got := statByName(t, res, name).Kind; got != kind {
			t.Errorf("%s kind = %s, want %s", name, got, kind)
		}
	}
}

func TestCallTreeRootShowsNetValueOfDiff(t *testing.T) {
	res, err := analyzer.Analyze(diffProfile(), analyzer.Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	// Base 200, new 200: no net change, though 100 moved.
	root := res.CallTree
	if root.CumulativeValue != 0 {
		t.Errorf("root cum = %d, want 0", root.CumulativeValue)
	}
	if a := childNamed(t, root, "A"); a.CumulativeValue != 50 {
		t.Errorf("A cum = %d, want 50", a.CumulativeValue)
	}
}

// Enough samples to split into several chunks, so the merge has to carry the
// root's own value (samples with an empty stack).
func TestCallTreeRootKeepsEmptyStackSamplesAcrossChunks(t *testing.T) {
	p := &parser.Profile{SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}}}
	for range 1000 {
		p.Samples = append(p.Samples,
			parser.Sample{Values: []int64{1}},
			parser.Sample{Stack: []parser.Location{frame("f", "f.js", 1)}, Values: []int64{1}})
	}
	res, err := analyzer.Analyze(p, analyzer.Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if root := res.CallTree; root.CumulativeValue != 2000 || childNamed(t, root, "f").CumulativeValue != 1000 {
		t.Errorf("root cum = %d, want 2000 with f at 1000", root.CumulativeValue)
	}
}

// -trimpath builds record module-relative filenames, and a dotless module may
// live under a src/ directory (as in the official golang image's /usr/src/app).
func TestCodeKindWithTrimpathAndDotlessModules(t *testing.T) {
	p := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{goFrame("runtime.mallocgc", "runtime/malloc.go", 900, 1000), goFrame("runtime.main", "runtime/proc.go", 140, 280)}, Values: []int64{30}},
			{Stack: []parser.Location{goFrame("github.com/x/y.(*T).F", "github.com/x/y@v1.2.3/y.go", 5, 6), goFrame("runtime.goexit", "runtime/asm_amd64.s", 1700, 1700)}, Values: []int64{20}},
			{Stack: []parser.Location{goFrame("myapp/handlers.Get", "myapp/handlers/get.go", 10, 12)}, Values: []int64{25}},
			{Stack: []parser.Location{goFrame("app/handlers.Get", "/usr/src/app/handlers/get.go", 10, 12)}, Values: []int64{25}},
		},
	}
	res, err := analyzer.Analyze(p, analyzer.Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	want := map[string]analyzer.CodeKind{
		"runtime.mallocgc":      analyzer.CodeLibrary,
		"github.com/x/y.(*T).F": analyzer.CodeLibrary,
		"myapp/handlers.Get":    analyzer.CodeApplication,
		"app/handlers.Get":      analyzer.CodeApplication,
	}
	for name, kind := range want {
		if got := statByName(t, res, name).Kind; got != kind {
			t.Errorf("%s kind = %s, want %s", name, got, kind)
		}
	}
}

func TestLabelsAddDirectoriesUntilDistinct(t *testing.T) {
	refs := []analyzer.FuncRef{
		{Key: "a", Name: "f", Filename: "/app/node_modules/x/index.js", Line: 10},
		{Key: "b", Name: "f", Filename: "/app/node_modules/y/index.js", Line: 10},
		{Key: "c", Name: "g", Filename: "/app/g.js", Line: 1},
		{Key: "a", Name: "f", Filename: "/app/node_modules/x/index.js", Line: 10},
	}
	got := strings.Join(analyzer.Labels(refs), " | ")
	want := "f (x/index.js:10) | f (y/index.js:10) | g | f (x/index.js:10)"
	if got != want {
		t.Errorf("Labels = %s, want %s", got, want)
	}
}

// Labels name a function by the line that identifies it, so a Go function
// reads the same in a call path and as a hotspot, whatever line inside it
// was hottest.
func TestGoLabelsUseTheFunctionsDeclarationLine(t *testing.T) {
	p := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{goFrame("G", "a/g.go", 5, 6), goFrame("main", "m.go", 1, 2)}, Values: []int64{60}},
			{Stack: []parser.Location{goFrame("G", "b/g.go", 5, 7), goFrame("G", "a/g.go", 5, 8), goFrame("main", "m.go", 1, 3)}, Values: []int64{40}},
		},
	}
	res, err := analyzer.Analyze(p, analyzer.Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	var refs []analyzer.FuncRef
	for _, h := range res.Hotspots {
		refs = append(refs, analyzer.FuncRef{Key: h.Key, Name: h.Name, Filename: h.Filename, Line: h.DeclLine})
		if h.Filename == "b/g.go" && (h.DeclLine != 5 || h.Line != 7) {
			t.Errorf("DeclLine/Line = %d/%d, want 5/7", h.DeclLine, h.Line)
		}
	}
	if got, want := strings.Join(analyzer.Labels(refs), " | "), "G (a/g.go:5) | G (b/g.go:5)"; got != want {
		t.Errorf("Labels = %s, want %s", got, want)
	}
	if n := childNamed(t, childNamed(t, childNamed(t, res.CallTree, "main"), "G"), "G"); n.Filename != "b/g.go" || n.DeclLine != 5 {
		t.Errorf("call-tree node = %s:%d, want b/g.go:5", n.Filename, n.DeclLine)
	}
}

func TestCodeKindOfWasmAndWindowsPaths(t *testing.T) {
	p := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{frame("strip", "wasm://wasm/00a1b2c3", 1)}, Values: []int64{10}},
			{Stack: []parser.Location{frame("parse", `C:\proj\node_modules\body-parser\lib\json.js`, 76)}, Values: []int64{10}},
			{Stack: []parser.Location{frame("handler", `C:\proj\src\server.js`, 3)}, Values: []int64{10}},
		},
	}
	res, err := analyzer.Analyze(p, analyzer.Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	want := map[string]analyzer.CodeKind{"strip": analyzer.CodeNative, "parse": analyzer.CodeLibrary, "handler": analyzer.CodeApplication}
	for name, kind := range want {
		if got := statByName(t, res, name).Kind; got != kind {
			t.Errorf("%s kind = %s, want %s", name, got, kind)
		}
	}
}

func TestGoDependencyNeedsAVersionAndSkipsTheMainModule(t *testing.T) {
	mod := "/root/go/pkg/mod/"
	p := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{
			// A binary from `go install example.com/tool@v1.4.0` runs its own
			// module out of the module cache.
			{Stack: []parser.Location{
				goFrame("example.com/tool/internal/x.F", mod+"example.com/tool@v1.4.0/internal/x/f.go", 1, 2),
				goFrame("main.main", mod+"example.com/tool@v1.4.0/main.go", 1, 2),
			}, Values: []int64{10}},
			{Stack: []parser.Location{goFrame("github.com/x/y.F", mod+"github.com/x/y@v1.2.3/y.go", 1, 2)}, Values: []int64{10}},
			{Stack: []parser.Location{goFrame("golang.org/x/net/http2.F", mod+"golang.org/x/net@v0.0.0-20240101000000-abcdef123456/http2/f.go", 1, 2)}, Values: []int64{10}},
			// "@v" in a plain directory name is not a module version.
			{Stack: []parser.Location{goFrame("svc/handlers.Get", "/home/u/work/svc@v2-fix/handlers/get.go", 1, 2)}, Values: []int64{10}},
		},
	}
	res, err := analyzer.Analyze(p, analyzer.Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	want := map[string]analyzer.CodeKind{
		"example.com/tool/internal/x.F": analyzer.CodeApplication,
		"main.main":                     analyzer.CodeApplication,
		"github.com/x/y.F":              analyzer.CodeLibrary,
		"golang.org/x/net/http2.F":      analyzer.CodeLibrary,
		"svc/handlers.Get":              analyzer.CodeApplication,
	}
	for name, kind := range want {
		if got := statByName(t, res, name).Kind; got != kind {
			t.Errorf("%s kind = %s, want %s", name, got, kind)
		}
	}
}

func TestLabelsShortenWindowsPaths(t *testing.T) {
	refs := []analyzer.FuncRef{
		{Key: "a", Name: "h", Filename: `C:\app\p\index.js`, Line: 10},
		{Key: "b", Name: "h", Filename: `C:\app\q\index.js`, Line: 10},
	}
	if got, want := strings.Join(analyzer.Labels(refs), " | "), "h (p/index.js:10) | h (q/index.js:10)"; got != want {
		t.Errorf("Labels = %s, want %s", got, want)
	}
}

// A server installed with `go install` may never sample package main: its
// main goroutine sleeps while others serve. The binary's file name, from the
// profile's first mapping, still names the main module.
func TestGoMainModuleFromBinaryName(t *testing.T) {
	mod := "/root/go/pkg/mod/"
	p := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Mappings:    []parser.Mapping{{ID: 1, File: "/root/go/bin/tool"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{goFrame("example.com/tool/internal/work.Busy", mod+"example.com/tool@v1.6.0/internal/work/busy.go", 1, 2)}, Values: []int64{10}},
			{Stack: []parser.Location{goFrame("example.com/x/tool/v2/run.Go", mod+"example.com/x/tool/v2@v2.1.0/run/go.go", 1, 2)}, Values: []int64{10}},
			{Stack: []parser.Location{goFrame("github.com/x/y.F", mod+"github.com/x/y@v1.2.3/y.go", 1, 2)}, Values: []int64{10}},
		},
	}
	res, err := analyzer.Analyze(p, analyzer.Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	want := map[string]analyzer.CodeKind{
		"example.com/tool/internal/work.Busy": analyzer.CodeApplication,
		"example.com/x/tool/v2/run.Go":        analyzer.CodeApplication,
		"github.com/x/y.F":                    analyzer.CodeLibrary,
	}
	for name, kind := range want {
		if got := statByName(t, res, name).Kind; got != kind {
			t.Errorf("%s kind = %s, want %s", name, got, kind)
		}
	}
}

// A binary built from local sources may share its name with a dependency
// (a "cli" tool using github.com/urfave/cli). Package main, or any local Go
// file, shows that the main module is not in the module cache, so the binary
// name must not claim the dependency.
func TestGoBinaryNameRuleNeedsNoOtherEvidence(t *testing.T) {
	dep := goFrame("github.com/urfave/cli.(*App).Run", "/root/go/pkg/mod/github.com/urfave/cli@v1.22.0/app.go", 1, 2)
	cases := map[string]parser.Location{
		"main sampled":  goFrame("main.main", "/home/u/src/cli/main.go", 1, 2),
		"local package": goFrame("cli/internal/work.Busy", "/home/u/src/cli/internal/work/w.go", 1, 2),
	}
	for name, caller := range cases {
		t.Run(name, func(t *testing.T) {
			p := &parser.Profile{
				SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
				Mappings:    []parser.Mapping{{ID: 1, File: "/home/u/bin/cli"}},
				Samples:     []parser.Sample{{Stack: []parser.Location{dep, caller}, Values: []int64{10}}},
			}
			res, err := analyzer.Analyze(p, analyzer.Options{})
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}
			if got := statByName(t, res, "github.com/urfave/cli.(*App).Run").Kind; got != analyzer.CodeLibrary {
				t.Errorf("dependency kind = %s, want library", got)
			}
		})
	}
}

// cgo wraps each C call in a function recorded in a generated file without a
// directory (_cgo_gotypes.go). It is neither local source code, which would
// turn off the binary-name fallback, nor application code: the cost is the
// C function's.
func TestCgoGeneratedFramesAreNative(t *testing.T) {
	mod := "/root/go/pkg/mod/"
	p := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Mappings:    []parser.Mapping{{ID: 1, File: "/root/go/bin/tool"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{
				goFrame("example.com/cdep._Cfunc_burn", "_cgo_gotypes.go", 40, 41),
				goFrame("example.com/tool/internal/work.Busy", mod+"example.com/tool@v1.7.0/internal/work/w.go", 1, 2),
			}, Values: []int64{60}},
			{Stack: []parser.Location{goFrame("example.com/dep/sq.Sum", mod+"example.com/dep@v1.2.3/sq/sum.go", 1, 2)}, Values: []int64{40}},
		},
	}
	res, err := analyzer.Analyze(p, analyzer.Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	want := map[string]analyzer.CodeKind{
		"example.com/cdep._Cfunc_burn":        analyzer.CodeNative,
		"example.com/tool/internal/work.Busy": analyzer.CodeApplication,
		"example.com/dep/sq.Sum":              analyzer.CodeLibrary,
	}
	for name, kind := range want {
		if got := statByName(t, res, name).Kind; got != kind {
			t.Errorf("%s kind = %s, want %s", name, got, kind)
		}
	}
}

func childNamed(t *testing.T, node *analyzer.CallTreeNode, name string) *analyzer.CallTreeNode {
	t.Helper()
	for _, c := range node.Children {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("%s has no child %s", node.Name, name)
	return nil
}

func TestPrimarySampleType(t *testing.T) {
	types := func(pairs ...string) []parser.SampleType {
		var st []parser.SampleType
		for i := 0; i < len(pairs); i += 2 {
			st = append(st, parser.SampleType{Type: pairs[i], Unit: pairs[i+1]})
		}
		return st
	}
	goHeap := types("alloc_objects", "count", "alloc_space", "bytes", "inuse_objects", "count", "inuse_space", "bytes")
	cases := []struct {
		name  string
		pt    parser.ProfileType
		types []parser.SampleType
		def   string
		want  int
	}{
		// Time, as pprof shows by default, rather than sample counts.
		{"V8 time", parser.ProfileCPU, types("sample", "count", "wall", "nanoseconds"), "", 1},
		{"V8 time with CPU time", parser.ProfileCPU, types("sample", "count", "wall", "nanoseconds", "cpu", "nanoseconds"), "", 2},
		{"Go CPU", parser.ProfileCPU, types("samples", "count", "cpu", "nanoseconds"), "", 1},
		{"counts only", parser.ProfileCPU, types("samples", "count"), "", 0},
		// Go's allocs profile names alloc_space as its default.
		{"Go allocs", parser.ProfileHeap, goHeap, "alloc_space", 1},
		// Otherwise the last type, as in pprof.
		{"Go heap", parser.ProfileHeap, goHeap, "", 3},
		{"V8 heap", parser.ProfileHeap, types("objects", "count", "space", "bytes"), "", 1},
		{"V8 heap with allocations", parser.ProfileHeap, types("inuse_objects", "count", "alloc_objects", "count", "inuse_space", "bytes", "alloc_space", "bytes"), "", 3},
		{"Go mutex", parser.ProfileMutex, types("contentions", "count", "delay", "nanoseconds"), "", 1},
	}
	for _, c := range cases {
		p := &parser.Profile{SampleTypes: c.types, DefaultSampleType: c.def}
		if got := analyzer.GetPrimarySampleTypeIndex(p, c.pt); got != c.want {
			t.Errorf("%s: index = %d, want %d", c.name, got, c.want)
		}
	}
}

// Samples without a value for the analyzed type, such as a heap profile's
// freed allocations for inuse_space, are not counted.
func TestTotalSamplesSkipsZeroValues(t *testing.T) {
	f := frame("f", "f.js", 1)
	p := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "alloc_space", Unit: "bytes"}, {Type: "inuse_space", Unit: "bytes"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{f}, Values: []int64{100, 100}},
			{Stack: []parser.Location{f}, Values: []int64{50, 0}},
			{Stack: []parser.Location{f}, Values: []int64{25, 0}},
		},
	}
	for idx, want := range []int{3, 1} {
		res, err := analyzer.Analyze(p, analyzer.Options{SampleTypeIndex: idx})
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		if res.TotalSamples != want {
			t.Errorf("%s: TotalSamples = %d, want %d", p.SampleTypes[idx].Type, res.TotalSamples, want)
		}
	}
}

// A sample record stands for every sample of one stack. Where the profile
// counts them (Go CPU, V8 time), the counts are the samples.
func TestSamplesComeFromTheSampleCount(t *testing.T) {
	f, g := frame("f", "f.js", 1), frame("g", "g.js", 1)
	p := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "sample", Unit: "count"}, {Type: "wall", Unit: "nanoseconds"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{f}, Values: []int64{5, 50}},
			{Stack: []parser.Location{g, f}, Values: []int64{3, 30}},
			// No time: not a sample of the analyzed type.
			{Stack: []parser.Location{g}, Values: []int64{2, 0}},
		},
	}
	res, err := analyzer.Analyze(p, analyzer.Options{SampleTypeIndex: 1})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if res.TotalSamples != 8 {
		t.Errorf("TotalSamples = %d, want 8", res.TotalSamples)
	}
	for name, want := range map[string]int{"f": 8, "g": 3} {
		if got := statByName(t, res, name).SampleCount; got != want {
			t.Errorf("%s SampleCount = %d, want %d", name, got, want)
		}
	}

	// The negated base samples of a diff count by magnitude.
	res, err = analyzer.Analyze(diffProfile(), analyzer.Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if res.TotalSamples != 400 {
		t.Errorf("diff TotalSamples = %d, want 400", res.TotalSamples)
	}
}

// SelfSampleCount goes with the self percent, SampleCount with the
// cumulative one.
func TestSelfSampleCount(t *testing.T) {
	f, g := frame("f", "f.js", 1), frame("g", "g.js", 1)
	p := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "sample", Unit: "count"}, {Type: "wall", Unit: "nanoseconds"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{f}, Values: []int64{2, 20}},
			{Stack: []parser.Location{g, f}, Values: []int64{3, 30}},
		},
	}
	res, err := analyzer.Analyze(p, analyzer.Options{SampleTypeIndex: 1})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if s := statByName(t, res, "f"); s.SelfSampleCount != 2 || s.SampleCount != 5 {
		t.Errorf("f self/all samples = %d/%d, want 2/5", s.SelfSampleCount, s.SampleCount)
	}
	for _, h := range res.Hotspots {
		if h.Name == "f" && h.SelfSampleCount != 2 {
			t.Errorf("f hotspot SelfSampleCount = %d, want 2", h.SelfSampleCount)
		}
	}
}

// A heap profile's bytes go with the object count of the same kind
// (alloc_objects with alloc_space, inuse_objects with inuse_space, objects
// with V8's space), which the analysis sums like the bytes. Only samples
// with bytes count, so freed allocations do not count for inuse_space.
func TestHeapBytesCarryTheirObjectCount(t *testing.T) {
	f, g := frame("f", "f.js", 1), frame("g", "g.js", 1)
	p := &parser.Profile{
		SampleTypes: []parser.SampleType{
			{Type: "inuse_objects", Unit: "count"}, {Type: "alloc_objects", Unit: "count"},
			{Type: "inuse_space", Unit: "bytes"}, {Type: "alloc_space", Unit: "bytes"},
		},
		Samples: []parser.Sample{
			{Stack: []parser.Location{f}, Values: []int64{1, 10, 100, 1000}},
			{Stack: []parser.Location{g, f}, Values: []int64{0, 20, 0, 2000}},
		},
	}
	cases := []struct {
		idx                       int
		value, objects            int64
		fSelf, fObjs, gSelf, gObj int64
	}{
		{3, 3000, 30, 10, 30, 20, 20}, // alloc_space with alloc_objects
		{2, 100, 1, 1, 1, 0, 0},       // inuse_space with inuse_objects
	}
	for _, c := range cases {
		res, err := analyzer.Analyze(p, analyzer.Options{SampleTypeIndex: c.idx})
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		name := p.SampleTypes[c.idx].Type
		if !res.CountsObjects || res.TotalValue != c.value || res.TotalObjects != c.objects {
			t.Errorf("%s: CountsObjects/TotalValue/TotalObjects = %v/%d/%d, want true/%d/%d",
				name, res.CountsObjects, res.TotalValue, res.TotalObjects, c.value, c.objects)
		}
		if s := statByName(t, res, "f"); s.SelfObjects != c.fSelf || s.Objects != c.fObjs {
			t.Errorf("%s: f self/all objects = %d/%d, want %d/%d", name, s.SelfObjects, s.Objects, c.fSelf, c.fObjs)
		}
		if c.gObj > 0 {
			if s := statByName(t, res, "g"); s.SelfObjects != c.gSelf || s.Objects != c.gObj {
				t.Errorf("%s: g self/all objects = %d/%d, want %d/%d", name, s.SelfObjects, s.Objects, c.gSelf, c.gObj)
			}
		}
	}

	// Objects analyzed by themselves are the value: there is no other count.
	res, err := analyzer.Analyze(p, analyzer.Options{SampleTypeIndex: 1})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if res.CountsObjects {
		t.Error("alloc_objects: CountsObjects = true, want false")
	}

	// V8's heap profile without allocations names its types space and objects.
	v8 := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "objects", Unit: "count"}, {Type: "space", Unit: "bytes"}},
		Samples:     []parser.Sample{{Stack: []parser.Location{f}, Values: []int64{4, 4096}}},
	}
	res, err = analyzer.Analyze(v8, analyzer.Options{SampleTypeIndex: 1})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !res.CountsObjects || res.TotalValue != 4096 || res.TotalObjects != 4 {
		t.Errorf("space: CountsObjects/TotalValue/TotalObjects = %v/%d/%d, want true/4096/4", res.CountsObjects, res.TotalValue, res.TotalObjects)
	}

	// A CPU profile has no objects.
	res, err = analyzer.Analyze(&parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}, {Type: "cpu", Unit: "nanoseconds"}},
		Samples:     []parser.Sample{{Stack: []parser.Location{f}, Values: []int64{1, 10}}},
	}, analyzer.Options{SampleTypeIndex: 1})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if res.CountsObjects || res.TotalObjects != 0 {
		t.Errorf("cpu: CountsObjects/TotalObjects = %v/%d, want false/0", res.CountsObjects, res.TotalObjects)
	}
}

// A hotspot carries the amounts behind its percentages: the values and,
// for heap bytes, the objects.
func TestHotspotCarriesItsAmounts(t *testing.T) {
	f, g := frame("f", "f.js", 1), frame("g", "g.js", 1)
	p := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "alloc_objects", Unit: "count"}, {Type: "alloc_space", Unit: "bytes"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{f}, Values: []int64{10, 1000}},
			{Stack: []parser.Location{g, f}, Values: []int64{20, 2000}},
		},
	}
	res, err := analyzer.Analyze(p, analyzer.Options{SampleTypeIndex: 1})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	var found bool
	for _, h := range res.CumulativeHotspots {
		if h.Name != "f" {
			continue
		}
		found = true
		if h.SelfValue != 1000 || h.CumulativeValue != 3000 || h.SelfObjects != 10 || h.Objects != 30 {
			t.Errorf("f hotspot values %d/%d, objects %d/%d, want 1000/3000, 10/30", h.SelfValue, h.CumulativeValue, h.SelfObjects, h.Objects)
		}
	}
	if !found {
		t.Error("f is not a cumulative hotspot")
	}
}
