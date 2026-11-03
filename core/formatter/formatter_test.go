package formatter_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pprof2md/core/analyzer"
	"pprof2md/core/formatter"
	"pprof2md/core/parser"
)

const profilesDir = "../../test/profiles"

func analyzeFixture(t *testing.T) (*analyzer.Result, parser.ProfileType) {
	t.Helper()
	p := filepath.Join(profilesDir, "cpu-test.pb.gz")
	if _, err := os.Stat(p); err != nil {
		t.Skipf("fixture cpu-test.pb.gz not found; run `npm run generate-profiles` in the reference repo")
	}
	prof, err := parser.ParseFile(p)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	pt := parser.DetectProfileType(prof)
	idx := analyzer.GetPrimarySampleTypeIndex(prof, pt)
	res, err := analyzer.Analyze(prof, analyzer.Options{SampleTypeIndex: idx})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	return res, pt
}

func render(t *testing.T, level formatter.Level) string {
	t.Helper()
	res, pt := analyzeFixture(t)
	out, err := formatter.Format(res, pt, level, formatter.Options{ProfileName: "cpu-test.pb.gz"})
	if err != nil {
		t.Fatalf("Format(%s): %v", level, err)
	}
	return out
}

func mustContain(t *testing.T, out, substr string) {
	t.Helper()
	if !strings.Contains(out, substr) {
		t.Errorf("output missing %q", substr)
	}
}

func TestSummaryStructure(t *testing.T) {
	out := render(t, formatter.LevelSummary)
	mustContain(t, out, "# PPROF Analysis:")
	mustContain(t, out, "**Profile:**")
	mustContain(t, out, "## Executive Summary")
	mustContain(t, out, "### Top self-time")
	mustContain(t, out, "### Top cumulative-time")
	// The summary is the executive summary alone, with nothing to link to.
	mustNotContain(t, out, "## Detailed Analysis")
	mustNotContain(t, out, "[Details]")
	mustNotContain(t, out, "<a id=")
}

func TestDetailStructure(t *testing.T) {
	out := render(t, formatter.LevelDetail)
	mustContain(t, out, "# PPROF Analysis:")
	mustContain(t, out, "## Executive Summary")
	mustContain(t, out, "### Top self-time")
	mustContain(t, out, "### Top cumulative-time")
	mustContain(t, out, "[Details](#")
	mustContain(t, out, "## Detailed Analysis")
	mustContain(t, out, `<a id="`)
	mustContain(t, out, "**Cumulative-time:**")
}

func TestFormatRoutingProducesDistinctOutputs(t *testing.T) {
	res, pt := analyzeFixture(t)
	opts := formatter.Options{ProfileName: "cpu-test.pb.gz"}

	summary, err := formatter.Format(res, pt, formatter.LevelSummary, opts)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := formatter.Format(res, pt, formatter.LevelDetail, opts)
	if err != nil {
		t.Fatal(err)
	}

	if summary == detail {
		t.Error("format levels should produce distinct outputs")
	}
}

// detailed is gone, and adaptive is now called detail.
func TestFormatUnknownLevelErrors(t *testing.T) {
	res, pt := analyzeFixture(t)
	for _, level := range []formatter.Level{"bogus", "detailed", "adaptive"} {
		if _, err := formatter.Format(res, pt, level, formatter.Options{}); err == nil {
			t.Errorf("Format(%s): expected an error for an unknown format level", level)
		}
	}
}

func TestMarkdownFormattingMarkers(t *testing.T) {
	out := render(t, formatter.LevelSummary)
	if !strings.Contains(out, "#") {
		t.Error("expected markdown headings")
	}
	if !strings.Contains(out, "**") {
		t.Error("expected bold markers")
	}
	if !strings.Contains(out, "`") {
		t.Error("expected inline code markers")
	}
}

func mustNotContain(t *testing.T, out, substr string) {
	t.Helper()
	if strings.Contains(out, substr) {
		t.Errorf("output unexpectedly contains %q", substr)
	}
}

func frame(name, filename string, line int) parser.Location {
	return parser.Location{Lines: []parser.LineInfo{{
		Line:     line,
		Function: parser.FunctionInfo{Name: name, Filename: filename},
	}}}
}

// cpuProfile: handleHTTP → processRequest fans out into a 60% branch
// (parseBody → JSON.parse) and a 30% branch (validateEmail → RegExp.exec).
func cpuProfile() *parser.Profile {
	pr, hh := frame("processRequest", "handler.ts", 88), frame("handleHTTP", "handler.ts", 25)
	return &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{frame("JSON.parse", "", 0), frame("parseBody", "handler.ts", 142), pr, hh}, Values: []int64{300}},
			{Stack: []parser.Location{frame("RegExp.exec", "", 0), frame("validateEmail", "handler.ts", 152), pr, hh}, Values: []int64{150}},
			{Stack: []parser.Location{pr, hh}, Values: []int64{50}},
		},
	}
}

// heapProfile: Buffer.alloc allocates the most bytes, Array.from allocates
// the most objects.
func heapProfile() *parser.Profile {
	hr := frame("handleRequest", "upload.ts", 8)
	return &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "alloc_objects", Unit: "count"}, {Type: "alloc_space", Unit: "bytes"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{frame("Buffer.alloc", "", 0), frame("resizeImage", "upload.ts", 47), frame("processUpload", "upload.ts", 22), hr}, Values: []int64{1200, 500_000_000}},
			{Stack: []parser.Location{frame("JSON.parse", "", 0), frame("parseRequestBody", "upload.ts", 12), hr}, Values: []int64{8000, 200_000_000}},
			{Stack: []parser.Location{frame("Array.from", "", 0), frame("transformUserData", "upload.ts", 15), hr}, Values: []int64{15000, 100_000_000}},
		},
	}
}

func renderProfile(t *testing.T, p *parser.Profile, level formatter.Level) string {
	t.Helper()
	return renderProfileWith(t, p, level, formatter.Options{})
}

func renderProfileWith(t *testing.T, p *parser.Profile, level formatter.Level, opts formatter.Options) string {
	t.Helper()
	pt := parser.DetectProfileType(p)
	res, err := analyzer.Analyze(p, analyzer.Options{SampleTypeIndex: analyzer.GetPrimarySampleTypeIndex(p, pt)})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	out, err := formatter.Format(res, pt, level, opts)
	if err != nil {
		t.Fatalf("Format(%s): %v", level, err)
	}
	return out
}

// The executive summary ranks the top functions by self cost and by
// cumulative cost. Among functions with the same cumulative cost, the one
// that does work itself comes first.
func TestSummaryIsTheExecutiveSummary(t *testing.T) {
	out := renderProfile(t, cpuProfile(), formatter.LevelSummary)
	want := "# PPROF Analysis: CPU\n" +
		"\n" +
		"**Profile:** `profile.pb`\n" +
		"**Samples:** 500 | **Type:** samples (count)\n" +
		"\n" +
		"## Executive Summary\n" +
		"\n" +
		"### Top self-time\n" +
		"\n" +
		"1. `JSON.parse` (**60.0%** of CPU)\n" +
		"2. `RegExp.exec` (**30.0%**)\n" +
		"3. `processRequest` (**10.0%**)\n" +
		"\n" +
		"### Top cumulative-time\n" +
		"\n" +
		"1. `processRequest` (**100.0%** of CPU)\n" +
		"2. `handleHTTP` (**100.0%**)\n" +
		"3. `JSON.parse` (**60.0%**)\n" +
		"4. `parseBody` (**60.0%**)\n" +
		"5. `RegExp.exec` (**30.0%**)"
	if out != want {
		t.Errorf("summary =\n%s\n\nwant\n%s", out, want)
	}
}

// Sections that the executive summary and the drill-down sections replace
// show in neither level.
func TestRemovedSectionsDoNotShow(t *testing.T) {
	for _, p := range []*parser.Profile{cpuProfile(), expressProfile(), libraryProfile()} {
		for _, level := range []formatter.Level{formatter.LevelSummary, formatter.LevelDetail} {
			out := renderProfile(t, p, level)
			for _, removed := range []string{
				"## Top Hotspots", "## Critical Paths", "## Key Observations",
				"Primary bottleneck", "Optimization potential",
				"**Call context:**", "**Insights:**",
			} {
				if strings.Contains(out, removed) {
					t.Errorf("%s output contains %q", level, removed)
				}
			}
		}
	}
}

// The executive summary links each function to its section, and a function
// in both rankings gets one section.
func TestDetailLinksEachRankedFunctionToOneSection(t *testing.T) {
	out := renderProfile(t, cpuProfile(), formatter.LevelDetail)
	mustContain(t, out, "## Executive Summary\n\n"+
		"### Top self-time\n\n"+
		"1. `JSON.parse` (**60.0%** of CPU) → [Details](#json-parse)\n"+
		"2. `RegExp.exec` (**30.0%**) → [Details](#regexp-exec)\n"+
		"3. `processRequest` (**10.0%**) → [Details](#processrequest)\n\n"+
		"### Top cumulative-time\n\n"+
		"1. `processRequest` (**100.0%** of CPU) → [Details](#processrequest)\n"+
		"2. `handleHTTP` (**100.0%**) → [Details](#handlehttp)\n"+
		"3. `JSON.parse` (**60.0%**) → [Details](#json-parse)\n"+
		"4. `parseBody` (**60.0%**) → [Details](#parsebody)\n"+
		"5. `RegExp.exec` (**30.0%**) → [Details](#regexp-exec)\n\n"+
		"---\n\n## Detailed Analysis\n\n")

	_, details, _ := strings.Cut(out, "## Detailed Analysis")
	var sections []string
	for _, line := range strings.Split(details, "\n") {
		if name, ok := strings.CutPrefix(line, "### "); ok {
			sections = append(sections, name)
		}
	}
	want := "`JSON.parse`,`RegExp.exec`,`processRequest`,`handleHTTP`,`parseBody`"
	if got := strings.Join(sections, ","); got != want {
		t.Errorf("sections = %s, want %s", got, want)
	}
	if n := strings.Count(out, `<a id="`); n != 5 {
		t.Errorf("%d anchors, want 5", n)
	}
	// validateEmail ranks sixth by cumulative time.
	mustNotContain(t, out, "`validateEmail` (**")
	mustNotContain(t, out, "### `validateEmail`")
}

func TestDetailSectionShowsSelfAndCumulativeTime(t *testing.T) {
	out := renderProfile(t, cpuProfile(), formatter.LevelDetail)
	mustContain(t, out, "<a id=\"processrequest\"></a>\n\n"+
		"### `processRequest`\n\n"+
		"**Location:** `handler.ts:88`\n"+
		"**Call path:** `(root)` → `handleHTTP` → `processRequest`\n"+
		"**Self-time:** 10.0% (50 samples)\n"+
		"**Cumulative-time:** 100.0% (500 samples)\n"+
		"**Type:** Application code\n\n"+
		"<a id=\"handlehttp\"></a>\n")
}

func TestHeapOutputUsesMemoryWording(t *testing.T) {
	summary := renderProfile(t, heapProfile(), formatter.LevelSummary)
	mustContain(t, summary, "### Top self-allocation\n\n1. `Buffer.alloc` (**62.5%** of allocated bytes)\n")
	mustContain(t, summary, "### Top cumulative-allocation\n\n1. `handleRequest` (**100.0%** of allocated bytes)\n")

	detail := renderProfile(t, heapProfile(), formatter.LevelDetail)
	mustContain(t, detail, "**Self-allocation:** 62.5%")
	mustContain(t, detail, "**Cumulative-allocation:** 62.5%")

	for _, out := range []string{summary, detail} {
		mustNotContain(t, out, "self-time")
		mustNotContain(t, out, "Self-time")
		mustNotContain(t, out, "cumulative-time")
		mustNotContain(t, out, "Cumulative-time")
		mustNotContain(t, out, "most time")
	}
}

// The samples next to a self percent are the function's self samples, and
// those next to a cumulative percent the samples whose stack contains it.
func TestSampleCountPluralization(t *testing.T) {
	out := renderProfile(t, cpuProfile(), formatter.LevelDetail)
	mustContain(t, out, "**Self-time:** 60.0% (300 samples)")
	mustContain(t, out, "**Self-time:** 10.0% (50 samples)")
	mustContain(t, out, "**Cumulative-time:** 100.0% (500 samples)")
}

func TestCallPathTellsSameNamedFunctionsApart(t *testing.T) {
	m := frame("main", "m.js", 1)
	p := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{
			// Two different getters that share a name, told apart by their
			// start lines.
			{Stack: []parser.Location{goFrame("get", "lib/readable.js", 244, 245), goFrame("get", "lib/readable.js", 147, 148), m}, Values: []int64{60}},
			// Recursion: the same function twice.
			{Stack: []parser.Location{frame("f", "f.js", 1), frame("f", "f.js", 1), m}, Values: []int64{40}},
		},
	}
	out := renderProfile(t, p, formatter.LevelDetail)
	mustContain(t, out, "1. `get (readable.js:244)` (**60.0%** of CPU) → [Details](#get-readable-js-244)\n")
	mustContain(t, out, "### `get (readable.js:244)`\n\n**Location:** `readable.js:245`\n"+
		"**Call path:** `(root)` → `main` → `get (readable.js:147)` → `get (readable.js:244)`\n")
	mustContain(t, out, "### `f`\n\n**Location:** `f.js:1`\n**Call path:** `(root)` → `main` → `f`\n")
}

func TestDrillDownCallPathFindsTheHotspotItself(t *testing.T) {
	m, work := frame("main", "m.js", 1), frame("work", "w.js", 1)
	p := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{
			// A same-named function on another line, reached through a
			// branch that sorts first.
			{Stack: []parser.Location{goFrame("handle", "router.js", 149, 150), frame("alpha", "m.js", 5), m}, Values: []int64{30}},
			{Stack: []parser.Location{goFrame("handle", "router.js", 434, 435), frame("zeta", "m.js", 9), m}, Values: []int64{40}},
			// work is reached through two branches; b is the hotter one.
			{Stack: []parser.Location{work, frame("a", "m.js", 20), m}, Values: []int64{10}},
			{Stack: []parser.Location{work, frame("b", "m.js", 30), m}, Values: []int64{20}},
		},
	}
	out := renderProfile(t, p, formatter.LevelDetail)
	// Each handle gets its own path; matching by name gave both alpha's.
	mustContain(t, out, "**Call path:** `(root)` → `main` → `zeta` → `handle`\n")
	mustContain(t, out, "**Call path:** `(root)` → `main` → `alpha` → `handle`\n")
	mustContain(t, out, "**Call path:** `(root)` → `main` → `b` → `work`\n")
}

// libraryProfile: a body parser in node_modules, called from a Node built-in
// before the handler runs, outweighs the application handler.
func libraryProfile() *parser.Profile {
	return &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{frame("parse", "/app/node_modules/body-parser/lib/types/json.js", 76), frame("onEnd", "node:internal/streams/end-of-stream", 40)}, Values: []int64{60}},
			{Stack: []parser.Location{frame("handler", "/app/src/server.ts", 12), frame("onEnd", "node:internal/streams/end-of-stream", 40)}, Values: []int64{40}},
		},
	}
}

func TestDetailTellsLibraryCodeFromApplicationCode(t *testing.T) {
	out := renderProfile(t, libraryProfile(), formatter.LevelDetail)
	mustContain(t, out, "### `parse`\n\n**Location:** `body-parser/lib/types/json.js:76`\n")
	mustContain(t, out, "**Type:** Runtime or library code")
	mustContain(t, out, "### `handler`\n\n**Location:** `server.ts:12`\n")
	mustContain(t, out, "**Type:** Application code")
}

func TestDetailSameNamedHotspotsGetDistinctSections(t *testing.T) {
	m := frame("main", "m.js", 1)
	p := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{goFrame("handle", "router.js", 434, 435), m}, Values: []int64{60}},
			{Stack: []parser.Location{goFrame("handle", "router.js", 149, 150), m}, Values: []int64{40}},
		},
	}
	out := renderProfile(t, p, formatter.LevelDetail)
	mustContain(t, out, "1. `handle (router.js:434)` (**60.0%** of CPU) → [Details](#handle-router-js-434)")
	mustContain(t, out, "2. `handle (router.js:149)` (**40.0%**) → [Details](#handle-router-js-149)")
	mustContain(t, out, "2. `handle (router.js:434)` (**60.0%**) → [Details](#handle-router-js-434)")
	mustContain(t, out, `<a id="handle-router-js-434"></a>`+"\n\n### `handle (router.js:434)`")
	mustContain(t, out, `<a id="handle-router-js-149"></a>`+"\n\n### `handle (router.js:149)`")
}

func TestDetailDrillIDsStayUniqueAfterTruncation(t *testing.T) {
	long := strings.Repeat("x", 40)
	p := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{frame(long+"A", "a.js", 1)}, Values: []int64{60}},
			{Stack: []parser.Location{frame(long+"B", "a.js", 2)}, Values: []int64{40}},
		},
	}
	out := renderProfile(t, p, formatter.LevelDetail)
	id := strings.Repeat("x", 30)
	mustContain(t, out, "[Details](#"+id+")")
	mustContain(t, out, "[Details](#"+id+"-2)")
}

// diffProfile mimics `go tool pprof -proto -diff_base`: base samples are
// negated and labeled pprof::base. Base: grow=20, shrink=80. New: grow=50,
// shrink=50.
func diffProfile() *parser.Profile {
	base := []parser.Label{{Key: "pprof::base", Str: "true"}}
	m := frame("main", "/app/main.go", 1)
	grow, shrink := frame("grow", "/app/grow.go", 1), frame("shrink", "/app/shrink.go", 1)
	return &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{grow, m}, Values: []int64{-20}, Labels: base},
			{Stack: []parser.Location{shrink, m}, Values: []int64{-80}, Labels: base},
			{Stack: []parser.Location{grow, m}, Values: []int64{50}},
			{Stack: []parser.Location{shrink, m}, Values: []int64{50}},
		},
	}
}

// A diff ranks changes by size. main's gains and losses cancel out, so it
// does not rank.
func TestExecutiveSummaryWordsDiffAsChange(t *testing.T) {
	for _, level := range []formatter.Level{formatter.LevelSummary, formatter.LevelDetail} {
		out := renderProfile(t, diffProfile(), level)
		mustContain(t, out, "### Largest self-time changes\n\n1. `grow` (**30.0%** of CPU)")
		mustContain(t, out, "2. `shrink` (**-30.0%**)")
		mustContain(t, out, "### Largest cumulative-time changes\n\n1. `grow` (**30.0%** of CPU)")
		mustNotContain(t, out, "`main` (**")
		mustNotContain(t, out, "### `main`")
		mustNotContain(t, out, "Top self-time")
		mustNotContain(t, out, "bottleneck")
	}
}

// movedProfile is a diff in which format's cost moved from oldPath to
// newPath: no function's self cost changed, but the callers' cumulative
// costs did.
func movedProfile() *parser.Profile {
	base := []parser.Label{{Key: "pprof::base", Str: "true"}}
	f := frame("format", "/app/format.go", 1)
	return &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{f, frame("oldPath", "/app/old.go", 1)}, Values: []int64{-5}, Labels: base},
			{Stack: []parser.Location{f, frame("newPath", "/app/new.go", 1)}, Values: []int64{5}},
		},
	}
}

func TestExecutiveSummaryWithoutSelfHotspots(t *testing.T) {
	out := renderProfile(t, movedProfile(), formatter.LevelSummary)
	mustContain(t, out, "## Executive Summary\n\n"+
		"### Largest self-time changes\n\n*No significant hotspots detected.*\n\n"+
		"### Largest cumulative-time changes\n\n1. `newPath` (**100.0%** of CPU)\n2. `oldPath` (**-100.0%**)")

	empty := &parser.Profile{SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}}}
	for _, level := range []formatter.Level{formatter.LevelSummary, formatter.LevelDetail} {
		out = renderProfile(t, empty, level)
		mustContain(t, out, "## Executive Summary\n\n*No significant bottlenecks detected.*")
		mustNotContain(t, out, "### Top")
		// No function ranks, so there is nothing to drill down into.
		mustNotContain(t, out, "## Detailed Analysis")
		mustNotContain(t, out, "---")
	}
}

func goFrame(name, filename string, startLine, line int) parser.Location {
	return parser.Location{Lines: []parser.LineInfo{{
		Line:     line,
		Function: parser.FunctionInfo{Name: name, Filename: filename, StartLine: startLine},
	}}}
}

// eightLeavesProfile: main.main calls eight leaves, each with 12.5% of the
// time.
func eightLeavesProfile() *parser.Profile {
	p := &parser.Profile{SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}}}
	for i := range 8 {
		p.Samples = append(p.Samples, parser.Sample{
			Stack:  []parser.Location{goFrame(fmt.Sprintf("main.leaf%d", i), "/app/main.go", 10*i+10, 10*i+11), goFrame("main.main", "/app/main.go", 1, 2)},
			Values: []int64{1},
		})
	}
	return p
}

// countItems counts the numbered items of a Markdown block.
func countItems(block string) int {
	n := 0
	for _, line := range strings.Split(block, "\n") {
		if i := strings.Index(line, ". `"); i > 0 && strings.Trim(line[:i], "0123456789") == "" {
			n++
		}
	}
	return n
}

func TestMaxCountsSetHowManyFunctionsRank(t *testing.T) {
	p := eightLeavesProfile()
	res, err := analyzer.Analyze(p, analyzer.Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	cases := []struct {
		maxSelf, maxCum   int
		self, cum, detail int
	}{
		{0, 0, 5, 5, 6}, // the default: leaves 0-4, and main.main with leaves 0-3
		{10, 2, 8, 2, 9},
		{1, 10, 1, 9, 9},
	}
	for _, c := range cases {
		opts := formatter.Options{MaxSelfCount: c.maxSelf, MaxCumulativeCount: c.maxCum}
		for _, level := range []formatter.Level{formatter.LevelSummary, formatter.LevelDetail} {
			out, err := formatter.Format(res, parser.ProfileCPU, level, opts)
			if err != nil {
				t.Fatalf("Format(%s): %v", level, err)
			}
			out, details, _ := strings.Cut(out, "## Detailed Analysis")
			self, cum, _ := strings.Cut(out, "### Top cumulative-time")
			if got := countItems(self); got != c.self {
				t.Errorf("%s with MaxSelfCount %d ranks %d functions by self-time, want %d", level, c.maxSelf, got, c.self)
			}
			if got := countItems(cum); got != c.cum {
				t.Errorf("%s with MaxCumulativeCount %d ranks %d functions by cumulative-time, want %d", level, c.maxCum, got, c.cum)
			}
			if level == formatter.LevelDetail {
				if got := strings.Count(details, `<a id="`); got != c.detail {
					t.Errorf("detail with counts %d/%d has %d sections, want %d", c.maxSelf, c.maxCum, got, c.detail)
				}
			}
		}
	}
}

// expressProfile mimics a V8 time profile of an Express app, as
// @datadog/pprof writes it: Node and Express call the route handler, which
// reads a file (readFileSync calls the native readFileUtf8). GC runs
// outside it.
func expressProfile() *parser.Profile {
	express := []parser.Location{
		frame("next", "/app/node_modules/router/lib/route.js", 119),
		frame("dispatch", "/app/node_modules/router/lib/route.js", 98),
		frame("handle", "/app/node_modules/router/index.js", 434),
		frame("handleRequest", "/app/node_modules/router/lib/layer.js", 142),
		frame("emit", "node:events", 489),
		frame("parserOnIncoming", "node:_http_server", 1267),
		frame("parserOnHeadersComplete", "node:_http_common", 77),
	}
	handler := frame("(anonymous:L#6:C#25)", "/app/server.ts", 6)
	stack := func(leaf ...parser.Location) []parser.Location {
		return append(append(leaf, handler), express...)
	}
	return &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "sample", Unit: "count"}, {Type: "wall", Unit: "nanoseconds"}},
		Samples: []parser.Sample{
			{Stack: stack(), Values: []int64{80, 80_000_000}},
			{Stack: stack(frame("readFileUtf8", "", 0), frame("readFileSync", "node:fs", 441)), Values: []int64{15, 15_000_000}},
			{Stack: []parser.Location{frame("Garbage Collection", "", 0), frame("Node.js", "", 0)}, Values: []int64{5, 5_000_000}},
		},
	}
}

func TestDetailHotspotShowsItsLocation(t *testing.T) {
	out := renderProfile(t, expressProfile(), formatter.LevelDetail)
	mustContain(t, out, "### `(anonymous:L#6:C#25)`\n\n**Location:** `server.ts:6`\n")
	// Native code has no file to point at.
	mustContain(t, out, "### `readFileUtf8`\n\n**Call path:**")
}

// A file is named by as much of its path as tells it apart from the other
// files in the profile.
func TestDetailLocationTellsSameNamedFilesApart(t *testing.T) {
	m := frame("main", "/app/src/main.ts", 1)
	p := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{frame("listUsers", "/app/src/users/index.ts", 12), m}, Values: []int64{60}},
			{Stack: []parser.Location{frame("listOrders", "/app/src/orders/index.ts", 30), m}, Values: []int64{40}},
		},
	}
	out := renderProfile(t, p, formatter.LevelDetail)
	mustContain(t, out, "### `listUsers`\n\n**Location:** `users/index.ts:12`\n")
	mustContain(t, out, "### `listOrders`\n\n**Location:** `orders/index.ts:30`\n")
}

func TestCallPathCollapsesRunsOfLibraryFrames(t *testing.T) {
	const express = "`parserOnHeadersComplete` → … 5 library frames … → `next` → `(anonymous:L#6:C#25)`"
	out := renderProfile(t, expressProfile(), formatter.LevelDetail)
	mustContain(t, out, "### `(anonymous:L#6:C#25)`\n\n**Location:** `server.ts:6`\n**Call path:** `(root)` → "+express+"\n")
	// A run of two frames has nothing in the middle to hide.
	mustContain(t, out, "### `readFileUtf8`\n\n**Call path:** `(root)` → "+express+" → `readFileSync` → `readFileUtf8`\n")
	mustContain(t, out, "### `Garbage Collection`\n\n**Call path:** `(root)` → `Node.js` → `Garbage Collection`\n")
}

// Collapsing hides at least three frames: fewer would save next to nothing.
func TestCallPathKeepsShortRunsOfLibraryFrames(t *testing.T) {
	p := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{{
			Stack: []parser.Location{
				frame("handler", "/app/server.js", 3),
				frame("d", "/app/node_modules/x/d.js", 1),
				frame("c", "/app/node_modules/x/c.js", 1),
				frame("b", "/app/node_modules/x/b.js", 1),
				frame("a", "/app/node_modules/x/a.js", 1),
			},
			Values: []int64{100},
		}},
	}
	out := renderProfile(t, p, formatter.LevelDetail)
	mustContain(t, out, "**Call path:** `(root)` → `a` → `b` → `c` → `d` → `handler`\n")
}

// A frame hidden in a collapsed run does not take part in labels.
func TestCollapsedChainLabelsTheShownFrames(t *testing.T) {
	p := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{{
			Stack: []parser.Location{
				frame("handle", "/app/node_modules/x/b.js", 9),
				frame("c", "/app/node_modules/x/c.js", 1),
				frame("handle", "/app/node_modules/x/a.js", 5),
				frame("d", "/app/node_modules/x/d.js", 1),
				frame("main", "/app/main.js", 1),
			},
			Values: []int64{100},
		}},
	}
	out := renderProfile(t, p, formatter.LevelDetail)
	mustContain(t, out, "**Call path:** `(root)` → `main` → `d` → `handle (a.js)` → `c` → `handle (b.js)`\n")

	p.Samples[0].Stack = append(p.Samples[0].Stack[:4:4], frame("e", "/app/node_modules/x/e.js", 1), frame("f", "/app/node_modules/x/f.js", 1), p.Samples[0].Stack[4])
	out = renderProfile(t, p, formatter.LevelDetail)
	mustContain(t, out, "**Call path:** `(root)` → `main` → `f` → … 4 library frames … → `handle`\n")
}

// A V8 time profile is a CPU profile: its time is what the analysis uses,
// and the words are those of CPU.
func TestV8TimeProfileIsACPUProfile(t *testing.T) {
	for _, level := range []formatter.Level{formatter.LevelSummary, formatter.LevelDetail} {
		out := renderProfile(t, expressProfile(), level)
		mustContain(t, out, "# PPROF Analysis: CPU\n")
		mustContain(t, out, "1. `(anonymous:L#6:C#25)` (**80.0%** of CPU)")
		mustContain(t, out, "### Top self-time\n")
		mustNotContain(t, out, "WALL TIME")
		mustNotContain(t, out, "wall time")
	}
}

// Library code is named by its package or Node module, which is what a
// reader needs to know about it; application code by its file.
func TestLocationsNameThePackageOfLibraryCode(t *testing.T) {
	m := frame("main", "/app/src/main.ts", 1)
	p := &parser.Profile{SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}}}
	for _, s := range []struct {
		leaf  parser.Location
		value int64
	}{
		{frame("update", "node:internal/crypto/hash", 184), 30},
		{frame("stringify", "/app/node_modules/express/lib/response.js", 1026), 20},
		{frame("parse", "/app/node_modules/@scope/pkg/lib/parse.js", 10), 15},
		{frame("inner", "/app/node_modules/a/node_modules/b/index.js", 5), 12},
		{frame("listUsers", "/app/src/users/index.js", 12), 10},
		{frame("listOrders", "/app/src/orders/index.js", 30), 8},
		{frame("gc", "", 0), 5},
	} {
		p.Samples = append(p.Samples, parser.Sample{Stack: []parser.Location{s.leaf, m}, Values: []int64{s.value}})
	}
	want := []struct{ name, loc string }{
		{"update", "node:internal/crypto/hash:184"},
		{"stringify", "express/lib/response.js:1026"},
		{"parse", "@scope/pkg/lib/parse.js:10"},
		{"inner", "b/index.js:5"},
		{"listUsers", "users/index.js:12"},
		{"listOrders", "orders/index.js:30"},
	}

	out := renderProfileWith(t, p, formatter.LevelDetail, formatter.Options{MaxSelfCount: 10})
	for _, w := range want {
		mustContain(t, out, "### `"+w.name+"`\n\n**Location:** `"+w.loc+"`\n")
	}
	mustContain(t, out, "### `gc`\n\n**Call path:**")
}

// sourceProfile: work, on line 5 of filename, does all the work.
func sourceProfile(filename string) *parser.Profile {
	return &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{frame("work", filename, 5), frame("main", "/nonexistent/main.js", 1)}, Values: []int64{100}},
		},
	}
}

// The detail level always shows the source it finds: from the file's own
// path when that exists, else under SourceDir (the current directory by
// default). The summary level shows none.
func TestDetailIncludesSource(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	for i := 1; i <= 10; i++ {
		lines = append(lines, fmt.Sprintf("line%d", i))
	}
	abs := filepath.Join(dir, "app.js")
	if err := os.WriteFile(abs, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	block := func(name string) string {
		return "**Type:** Application code\n\n**Source:**\n\n```\n// " + name + ":2-8\n" +
			"line2\nline3\nline4\nline5 // ← HOT\nline6\nline7\nline8\n```\n"
	}

	out := renderProfile(t, sourceProfile(abs), formatter.LevelDetail)
	mustContain(t, out, block(abs))
	mustNotContain(t, renderProfile(t, sourceProfile(abs), formatter.LevelSummary), "**Source:**")

	out = renderProfileWith(t, sourceProfile("app.js"), formatter.LevelDetail, formatter.Options{SourceDir: dir})
	mustContain(t, out, block("app.js"))

	// The current directory is the default.
	t.Chdir(dir)
	out = renderProfile(t, sourceProfile("app.js"), formatter.LevelDetail)
	mustContain(t, out, block("app.js"))
}

// heapHeader is the header of a heap profile, up to the executive summary.
func heapHeader(out string) string {
	header, _, _ := strings.Cut(out, "## Executive Summary")
	return header
}

// A heap profile's header says what its percentages are shares of, in words,
// and the total amount: the records of a heap profile are not samples, and
// it has no duration.
func TestHeapHeaderSaysWhatItMeasures(t *testing.T) {
	alloc := "# PPROF Analysis: HEAP\n\n" +
		"**Profile:** `profile.pb`\n" +
		"**Measures:** allocated bytes (`alloc_space`): all memory allocated during profiling, including memory freed since\n" +
		"**Total:** 762.9 MB in 24,200 objects\n\n"
	for _, level := range []formatter.Level{formatter.LevelSummary, formatter.LevelDetail} {
		out := renderProfile(t, heapProfile(), level)
		if got := heapHeader(out); got != alloc {
			t.Errorf("%s header =\n%s\nwant\n%s", level, got, alloc)
		}
		mustNotContain(t, out, "N/A")
		mustNotContain(t, out, "sample")
	}

	// V8's heap profile without allocations holds what is still in use.
	inuse := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "objects", Unit: "count"}, {Type: "space", Unit: "bytes"}},
		Samples:     []parser.Sample{{Stack: []parser.Location{frame("cache", "/app/cache.js", 3)}, Values: []int64{4, 4096}}},
	}
	out := renderProfile(t, inuse, formatter.LevelDetail)
	mustContain(t, out, "**Measures:** in-use bytes (`space`): memory allocated and not yet freed when the profile was taken\n"+
		"**Total:** 4.0 KB in 4 objects\n")
	mustContain(t, out, "1. `cache` (**100.0%** of in-use bytes)")

	// A known duration shows.
	p := heapProfile()
	p.DurationNanos = 2_500_000_000
	mustContain(t, renderProfile(t, p, formatter.LevelSummary), "**Measures:** allocated bytes (`alloc_space`): all memory allocated during profiling, including memory freed since\n"+
		"**Duration:** 2.5s | **Total:** 762.9 MB in 24,200 objects\n")
}

// A heap profile shows the amount behind each percentage: the bytes and the
// objects, or the objects when they are what the analysis measures.
func TestHeapShowsBytesAndObjects(t *testing.T) {
	out := renderProfile(t, heapProfile(), formatter.LevelDetail)
	mustContain(t, out, "### `Buffer.alloc`\n\n**Call path:** `(root)` → `handleRequest` → `processUpload` → `resizeImage` → `Buffer.alloc`\n"+
		"**Self-allocation:** 62.5% (476.8 MB, 1,200 objects)\n**Cumulative-allocation:** 62.5% (476.8 MB, 1,200 objects)\n")
	mustContain(t, out, "**Self-allocation:** 0.0% (0 B, 0 objects)\n**Cumulative-allocation:** 100.0% (762.9 MB, 24,200 objects)\n")

	// Objects analyzed by themselves.
	p := heapProfile()
	res, err := analyzer.Analyze(p, analyzer.Options{SampleTypeIndex: 0})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	out, err = formatter.Format(res, parser.ProfileHeap, formatter.LevelDetail, formatter.Options{})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	mustContain(t, out, "**Measures:** allocated objects (`alloc_objects`): all objects allocated during profiling, including objects freed since\n"+
		"**Total:** 24,200 objects\n")
	mustContain(t, out, "1. `Array.from` (**62.0%** of allocated objects)")
	mustContain(t, out, "**Self-allocation:** 62.0% (15,000 objects)\n")

	// Bytes without an object count.
	p = heapProfile()
	p.SampleTypes = p.SampleTypes[1:]
	for i := range p.Samples {
		p.Samples[i].Values = p.Samples[i].Values[1:]
	}
	out = renderProfile(t, p, formatter.LevelDetail)
	mustContain(t, out, "**Total:** 762.9 MB\n")
	mustContain(t, out, "**Self-allocation:** 62.5% (476.8 MB)\n")
}

// A heap diff shows signed changes. Its percentages are shares of the base,
// so there is no total to show.
func TestHeapDiffShowsSignedAmounts(t *testing.T) {
	base := []parser.Label{{Key: "pprof::base", Str: "true"}}
	f := frame("f", "/app/f.go", 1)
	p := &parser.Profile{
		SampleTypes: []parser.SampleType{{Type: "alloc_objects", Unit: "count"}, {Type: "alloc_space", Unit: "bytes"}},
		Samples: []parser.Sample{
			{Stack: []parser.Location{f}, Values: []int64{-10, -100 << 20}, Labels: base},
			{Stack: []parser.Location{f}, Values: []int64{30, 300 << 20}},
		},
	}
	out := renderProfile(t, p, formatter.LevelDetail)
	mustNotContain(t, out, "**Total:**")
	mustContain(t, out, "**Self-allocation:** 200.0% (200.0 MB, 20 objects)\n")
}

// Both levels share one header, which names the sample type and leaves out
// a duration the profile does not record.
func TestHeaderNamesTheSampleTypeAndAKnownDuration(t *testing.T) {
	for _, level := range []formatter.Level{formatter.LevelSummary, formatter.LevelDetail} {
		out := renderProfile(t, cpuProfile(), level)
		mustContain(t, out, "**Profile:** `profile.pb`\n**Samples:** 500 | **Type:** samples (count)\n\n")
		mustNotContain(t, out, "N/A")

		p := cpuProfile()
		p.DurationNanos = 65_000_000_000
		mustContain(t, renderProfile(t, p, level), "**Profile:** `profile.pb`\n**Duration:** 1m 5s | **Samples:** 500 | **Type:** samples (count)\n\n")
	}
}

// A CPU profile whose samples carry a time shows the total of that time next
// to the sample count.
func TestHeaderShowsTotalSampleTime(t *testing.T) {
	for _, level := range []formatter.Level{formatter.LevelSummary, formatter.LevelDetail} {
		p := cpuProfile()
		p.SampleTypes = []parser.SampleType{{Type: "cpu", Unit: "nanoseconds"}}
		for i := range p.Samples {
			p.Samples[i].Values[0] *= 10_000_000
		}
		mustContain(t, renderProfile(t, p, level), "**Samples:** 3 | **Total sample time:** 5.0s | **Type:** cpu (nanoseconds)\n")
	}
}
