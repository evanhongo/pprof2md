package analyzer

import (
	"fmt"
	"path"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"

	"pprof2md/core/parser"
)

// Analyze computes the full set of statistics for a profile. Statistics
// aggregation and call-tree construction run concurrently, and each internally
// parallelizes across sample chunks.
func Analyze(p *parser.Profile, opts Options) (*Result, error) {
	idx := opts.SampleTypeIndex
	if idx < 0 || idx >= len(p.SampleTypes) {
		return nil, fmt.Errorf("sample type index %d not found", idx)
	}
	sampleType := p.SampleTypes[idx]

	cls := newClassifier(p)
	objectsIdx := objectsTypeIndex(p, idx)

	// Aggregation and tree construction are independent: run them in parallel.
	var (
		stats map[string]*FunctionStats
		sums  totals
		tree  *CallTreeNode
		wg    sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		stats, sums = aggregateFunctionStats(p, idx, samplesTypeIndex(p), objectsIdx, cls)
	}()
	go func() {
		defer wg.Done()
		tree = buildCallTree(p, idx, cls)
	}()
	wg.Wait()

	// Percentages need the total, known once every chunk is merged. A
	// profile without values has no functions to finish.
	total := sums.denominator()
	if total != 0 {
		finalizeStats(stats, total)
	}

	return &Result{
		SampleType:         sampleType,
		TotalSamples:       sums.samples,
		TotalValue:         total,
		CountsObjects:      objectsIdx >= 0,
		TotalObjects:       sums.objects,
		FunctionStats:      stats,
		CallTree:           tree,
		Hotspots:           identifyHotspots(stats),
		CumulativeHotspots: identifyCumulativeHotspots(stats),
		Diff:               sums.diff,
		DurationNanos:      p.DurationNanos,
	}, nil
}

// totals sums the samples that have a value, by magnitude: all of them, and
// the base ones of a diff, with the objects they allocated. diff is set when
// some value is negative.
type totals struct {
	all, base, objects int64
	samples            int
	hasBase, diff      bool
}

func (t *totals) merge(o totals) {
	t.all += o.all
	t.base += o.base
	t.objects += o.objects
	t.samples += o.samples
	t.hasBase = t.hasBase || o.hasBase
	t.diff = t.diff || o.diff
}

// denominator returns the value that cost percentages are relative to.
//
// In a diff profile (`go tool pprof -proto -diff_base`) the base samples are
// negated and labeled pprof::base, and, as in pprof, percentages are relative
// to the base total. Otherwise values are summed by magnitude: the plain sum
// for an ordinary profile, and still a bound that keeps percentages within
// ±100% when some values are negative.
func (t totals) denominator() int64 {
	if t.hasBase {
		return t.base
	}
	return t.all
}

// isBaseSample reports whether pprof marked the sample as coming from the
// base profile of a diff.
func isBaseSample(s *parser.Sample) bool {
	for _, l := range s.Labels {
		if l.Key == "pprof::base" && l.Str == "true" {
			return true
		}
	}
	return false
}

// --- concurrency helpers ---

type sampleRange struct{ lo, hi int }

func workerCount() int {
	n := runtime.GOMAXPROCS(0)
	if n < 1 {
		n = 1
	}
	return n
}

// chunkRanges splits n items into contiguous ranges, bounded by worker count
// and a minimum chunk size so tiny profiles are not over-sharded.
func chunkRanges(n, workers int) []sampleRange {
	if n == 0 {
		return nil
	}
	const minChunk = 256
	if workers < 1 {
		workers = 1
	}
	maxWorkers := (n + minChunk - 1) / minChunk
	if maxWorkers < 1 {
		maxWorkers = 1
	}
	if workers > maxWorkers {
		workers = maxWorkers
	}
	chunkSize := (n + workers - 1) / workers
	ranges := make([]sampleRange, 0, workers)
	for lo := 0; lo < n; lo += chunkSize {
		hi := lo + chunkSize
		if hi > n {
			hi = n
		}
		ranges = append(ranges, sampleRange{lo, hi})
	}
	return ranges
}

// --- function statistics (map-reduce) ---

// aggregateFunctionStats computes per-function costs for the value at idx,
// and the totals of the profile. When samplesIdx >= 0, that value counts the
// samples each record stands for; otherwise a record is one sample. When
// objectsIdx >= 0, that value counts the objects each record allocated.
// Percentages are left to finalizeStats.
func aggregateFunctionStats(p *parser.Profile, idx, samplesIdx, objectsIdx int, cls *classifier) (map[string]*FunctionStats, totals) {
	ranges := chunkRanges(len(p.Samples), workerCount())
	partials := make([]map[string]*FunctionStats, len(ranges))
	partialTotals := make([]totals, len(ranges))

	var wg sync.WaitGroup
	for ci, r := range ranges {
		wg.Add(1)
		go func(ci int, r sampleRange) {
			defer wg.Done()
			partials[ci], partialTotals[ci] = aggregateChunk(p.Samples[r.lo:r.hi], idx, samplesIdx, objectsIdx, cls)
		}(ci, r)
	}
	wg.Wait()

	stats := make(map[string]*FunctionStats)
	var sums totals
	for ci, partial := range partials {
		mergeStats(stats, partial)
		sums.merge(partialTotals[ci])
	}
	return stats, sums
}

// finalizeStats fills in percentages and each function's hottest line.
func finalizeStats(stats map[string]*FunctionStats, totalValue int64) {
	totalNum := float64(totalValue)
	for _, s := range stats {
		s.SelfPercent = float64(s.SelfValue) / totalNum * 100
		s.CumulativePercent = float64(s.CumulativeValue) / totalNum * 100
		s.Line = s.lines.hottest()
		s.lines = lineCosts{}
	}
}

func aggregateChunk(samples []parser.Sample, idx, samplesIdx, objectsIdx int, cls *classifier) (map[string]*FunctionStats, totals) {
	stats := make(map[string]*FunctionStats)

	getOrCreate := func(f *frame) *FunctionStats {
		s := stats[f.ref.Key]
		if s == nil {
			s = &FunctionStats{
				Key:      f.ref.Key,
				Name:     f.ref.Name,
				Filename: f.ref.Filename,
				Line:     f.line,
				DeclLine: f.ref.Line,
				Kind:     f.kind,
			}
			stats[f.ref.Key] = s
		}
		return s
	}

	var (
		sums   totals
		cache  = newFrameCache(cls)
		frames []*frame
		seen   = map[string]bool{} // the functions met so far on the stack
	)
	for si := range samples {
		sample := &samples[si]
		value := sampleValue(sample, idx)
		if value == 0 {
			continue
		}
		absValue := abs(value)
		n := 1 // samples the record stands for
		if samplesIdx >= 0 {
			n = int(abs(sampleValue(sample, samplesIdx)))
		}
		var objects int64 // objects the record allocated
		if objectsIdx >= 0 {
			objects = sampleValue(sample, objectsIdx)
		}

		sums.samples += n
		sums.objects += abs(objects)
		sums.all += absValue
		sums.diff = sums.diff || value < 0
		if isBaseSample(sample) {
			sums.hasBase = true
			sums.base += absValue
		}

		clear(seen)

		// frames is leaf-first: index 0 is the innermost frame.
		frames = cache.appendFrames(frames[:0], sample)
		for i, f := range frames {
			s := getOrCreate(f)

			if i == 0 {
				s.SelfValue += value
				s.SelfSampleCount += n
				s.SelfObjects += objects
				s.lines.addSelf(f.line, value)
			}
			// A recursive function counts once per sample.
			if !seen[s.Key] {
				seen[s.Key] = true
				s.CumulativeValue += value
				s.lines.addCum(f.line, value)
				s.SampleCount += n
				s.Objects += objects
			}
		}
	}

	return stats, sums
}

func mergeStats(dst, src map[string]*FunctionStats) {
	for key, s := range src {
		d := dst[key]
		if d == nil {
			dst[key] = s
			continue
		}
		d.SelfValue += s.SelfValue
		d.CumulativeValue += s.CumulativeValue
		d.SampleCount += s.SampleCount
		d.SelfSampleCount += s.SelfSampleCount
		d.Objects += s.Objects
		d.SelfObjects += s.SelfObjects
		d.lines.merge(s.lines)
	}
}

// --- call tree (parallel partial trees + merge) ---

// buildCallTree merges the samples into a call tree.
func buildCallTree(p *parser.Profile, idx int, cls *classifier) *CallTreeNode {
	ranges := chunkRanges(len(p.Samples), workerCount())
	partials := make([]*CallTreeNode, len(ranges))

	var wg sync.WaitGroup
	for ci, r := range ranges {
		wg.Add(1)
		go func(ci int, r sampleRange) {
			defer wg.Done()
			partials[ci] = buildChunkTree(p.Samples[r.lo:r.hi], idx, cls)
		}(ci, r)
	}
	wg.Wait()

	root := newNode("(root)")
	for _, partial := range partials {
		if partial != nil {
			mergeTree(root, partial)
		}
	}
	return root
}

func buildChunkTree(samples []parser.Sample, idx int, cls *classifier) *CallTreeNode {
	root := newNode("(root)")

	var (
		cache  = newFrameCache(cls)
		frames []*frame
	)
	for si := range samples {
		sample := &samples[si]
		value := sampleValue(sample, idx)
		if value == 0 {
			continue
		}

		cur := root
		cur.CumulativeValue += value
		// Walk from root to leaf (reverse of the leaf-first frames).
		frames = cache.appendFrames(frames[:0], sample)
		for i := len(frames) - 1; i >= 0; i-- {
			f := frames[i]
			child := cur.Children[f.ref.Key]
			if child == nil {
				child = &CallTreeNode{
					Key:      f.ref.Key,
					Name:     f.ref.Name,
					Filename: f.ref.Filename,
					DeclLine: f.ref.Line,
					Children: map[string]*CallTreeNode{},
				}
				cur.Children[f.ref.Key] = child
			}
			cur = child
			cur.CumulativeValue += value
		}
	}

	return root
}

// mergeTree adds src's values into dst, node by node.
func mergeTree(dst, src *CallTreeNode) {
	dst.CumulativeValue += src.CumulativeValue
	for key, sc := range src.Children {
		dc := dst.Children[key]
		if dc == nil {
			dc = &CallTreeNode{
				Key:      sc.Key,
				Name:     sc.Name,
				Filename: sc.Filename,
				DeclLine: sc.DeclLine,
				Children: map[string]*CallTreeNode{},
			}
			dst.Children[key] = dc
		}
		mergeTree(dc, sc)
	}
}

// --- small helpers ---

// funcRef identifies the function a frame belongs to as
// name@filename:StartLine, so all the lines sampled inside a function make
// one entry. A profile that leaves StartLine at 0 (V8 exporters such as
// @datadog/pprof, older Go releases, some Rust and Java exporters) has 0 in
// the key: the function's start is unknown, and same-named functions in one
// file make one entry.
func funcRef(ln parser.LineInfo) FuncRef {
	fn := ln.Function
	return FuncRef{
		Key:      fmt.Sprintf("%s@%s:%d", fn.Name, fn.Filename, fn.StartLine),
		Name:     fn.Name,
		Filename: fn.Filename,
		Line:     fn.StartLine,
	}
}

// displayLine is the source line to show for a frame, falling back to the
// function's start line when the profile has none.
func displayLine(ln parser.LineInfo) int {
	if ln.Line != 0 {
		return ln.Line
	}
	return ln.Function.StartLine
}

// lineCosts tallies a function's cost per source line while aggregating, so
// the hottest line can be picked once all chunks are merged.
//
// A function rarely has more than a few sampled lines, so a slice with a
// linear search is smaller and faster than maps here. Past lineIndexAt lines,
// a map index takes over the search.
type lineCosts struct {
	lines   []lineCost
	index   map[int]int // line → position in lines; nil until lineIndexAt
	hasSelf bool
}

const lineIndexAt = 64

type lineCost struct {
	line      int
	self, cum int64
}

func (c *lineCosts) at(line int) *lineCost {
	if c.index != nil {
		if i, ok := c.index[line]; ok {
			return &c.lines[i]
		}
	} else {
		for i := range c.lines {
			if c.lines[i].line == line {
				return &c.lines[i]
			}
		}
	}
	c.lines = append(c.lines, lineCost{line: line})
	if c.index != nil {
		c.index[line] = len(c.lines) - 1
	} else if len(c.lines) > lineIndexAt {
		c.index = make(map[int]int, 2*len(c.lines))
		for i, lc := range c.lines {
			c.index[lc.line] = i
		}
	}
	return &c.lines[len(c.lines)-1]
}

func (c *lineCosts) addSelf(line int, v int64) {
	c.at(line).self += v
	c.hasSelf = true
}

func (c *lineCosts) addCum(line int, v int64) {
	c.at(line).cum += v
}

func (c *lineCosts) merge(o lineCosts) {
	for _, lc := range o.lines {
		e := c.at(lc.line)
		e.self += lc.self
		e.cum += lc.cum
	}
	c.hasSelf = c.hasSelf || o.hasSelf
}

// hottest returns the line with the most self cost, or the most cumulative
// cost when there is no self cost. Costs compare by magnitude, so a diff
// profile picks the line that changed most. Ties go to the lowest line. It is
// 0 when nothing was tallied, which a function never is: its first frame
// tallies a cumulative cost.
func (c *lineCosts) hottest() int {
	best, bestV := 0, int64(-1)
	for _, lc := range c.lines {
		v := lc.cum
		if c.hasSelf {
			v = lc.self
		}
		if v = abs(v); v > bestV || (v == bestV && lc.line < best) {
			best, bestV = lc.line, v
		}
	}
	return best
}

// frame is one stack frame with its function resolved.
type frame struct {
	ref  FuncRef
	kind CodeKind
	line int // see displayLine
}

// frameCache resolves each location line once. Samples share locations, and
// their Stack entries share the Lines arrays, so the line's address
// identifies it.
type frameCache struct {
	frames map[*parser.LineInfo]*frame
	cls    *classifier
}

func newFrameCache(cls *classifier) *frameCache {
	return &frameCache{frames: map[*parser.LineInfo]*frame{}, cls: cls}
}

// appendFrames appends the sample's frames to dst in leaf-first order,
// expanding inlined lines. Within a location pprof lists lines
// innermost-first, so a plain walk over Stack then Lines keeps that order.
func (c *frameCache) appendFrames(dst []*frame, s *parser.Sample) []*frame {
	for i := range s.Stack {
		lines := s.Stack[i].Lines
		for j := range lines {
			ln := &lines[j]
			f := c.frames[ln]
			if f == nil {
				f = &frame{
					ref:  funcRef(*ln),
					kind: c.cls.kind(ln.Function.Name, ln.Function.Filename),
					line: displayLine(*ln),
				}
				c.frames[ln] = f
			}
			dst = append(dst, f)
		}
	}
	return dst
}

// classifier sorts the functions of one profile into kinds of code.
type classifier struct {
	// mainModuleDirs are versioned module directories (".../tool@v1.4.0/")
	// of the main module. A binary built by `go install tool@v1.4.0` runs its
	// own module out of the module cache, and that is application code even
	// though its path carries a version.
	mainModuleDirs []string
}

// newClassifier finds the main module's versioned directories: those that
// hold package main or, when the profile shows no other trace of the main
// module (no package main, no local Go file), those whose module is named
// like the executable (the first mapping). The fallback covers a server
// whose main goroutine only waits; it stays off otherwise, since a local
// binary may share its name with a dependency (a "cli" tool using
// github.com/urfave/cli).
func newClassifier(p *parser.Profile) *classifier {
	c := &classifier{}
	var (
		binary              string
		binaryDirs          []string
		hasMain, hasLocalGo bool
	)
	if len(p.Mappings) > 0 && p.Mappings[0].File != "" {
		binary = strings.TrimSuffix(path.Base(slashed(p.Mappings[0].File)), ".exe")
	}
	addDir := func(dirs *[]string, dir string) {
		if !slices.Contains(*dirs, dir) {
			*dirs = append(*dirs, dir)
		}
	}
	add := func(fn parser.FunctionInfo) {
		isMain := strings.HasPrefix(fn.Name, "main.")
		hasMain = hasMain || isMain
		filename := slashed(fn.Filename)
		var loc []int
		if strings.Contains(filename, "@v") { // cheap test before the regexp
			loc = goModuleVersion.FindStringIndex(filename)
		}
		if loc == nil {
			if isGoSource(filename) && !isCgoGenerated(filename) && !strings.Contains(filename, "/vendor/") && !isGoStdlib(fn.Name, filename) {
				hasLocalGo = true
			}
			return
		}
		dir := filename[:loc[1]+1]
		switch {
		case isMain:
			addDir(&c.mainModuleDirs, dir)
		case binary != "" && goBinaryName(filename[:loc[0]]) == binary:
			addDir(&binaryDirs, dir)
		}
	}
	for _, fn := range p.Functions {
		add(fn)
	}
	if len(p.Functions) == 0 { // a profile built in code, not parsed
		for i := range p.Samples {
			for _, loc := range p.Samples[i].Stack {
				for _, ln := range loc.Lines {
					add(ln.Function)
				}
			}
		}
	}
	if !hasMain && !hasLocalGo {
		c.mainModuleDirs = append(c.mainModuleDirs, binaryDirs...)
	}
	return c
}

// goBinaryName is the name `go install` gives the binary of a module path:
// its last element, skipping a major-version suffix ("example.com/tool/v2"
// installs "tool").
func goBinaryName(modulePath string) string {
	elems := strings.Split(modulePath, "/")
	name := elems[len(elems)-1]
	if len(elems) > 1 && goMajorVersion.MatchString(name) {
		name = elems[len(elems)-2]
	}
	return name
}

var goMajorVersion = regexp.MustCompile(`^v[0-9]+$`)

// kind classifies a function. Without a readable source file (none,
// WebAssembly, or a cgo wrapper around a C call) it is native. Runtime and
// dependency code is recognized as Node built-ins (node:*), node_modules,
// vendor/, Go modules (whose paths carry a module version) and the Go
// standard library. Everything else is application code.
func (c *classifier) kind(name, filename string) CodeKind {
	filename = slashed(filename)
	switch {
	case filename == "", strings.HasPrefix(filename, "wasm://"), isCgoGenerated(filename):
		return CodeNative
	case strings.HasPrefix(filename, "node:"),
		strings.Contains(filename, "/node_modules/"),
		strings.Contains(filename, "/vendor/"),
		c.isGoDependency(filename),
		isGoStdlib(name, filename):
		return CodeLibrary
	}
	return CodeApplication
}

// slashed turns Windows path separators into slashes.
func slashed(filename string) string {
	return strings.ReplaceAll(filename, `\`, "/")
}

// isCgoGenerated reports whether a file was generated by cgo, such as
// _cgo_gotypes.go, which holds the Go wrapper (_Cfunc_*) of each C function
// and is recorded without a directory.
func isCgoGenerated(filename string) bool {
	return strings.HasPrefix(path.Base(filename), "_cgo_")
}

func isGoSource(filename string) bool {
	return strings.HasSuffix(filename, ".go") || strings.HasSuffix(filename, ".s")
}

// goModuleVersion matches the version in a module directory name, as in
// "github.com/x/y@v1.2.3" or a pseudo-version "@v0.0.0-20240101000000-abc".
var goModuleVersion = regexp.MustCompile(`@v[0-9]+\.[0-9]+\.[0-9]+[^/]*`)

// isGoDependency reports whether a Go file belongs to a required module:
// both the module cache (.../pkg/mod/github.com/x/y@v1.2.3/y.go) and
// -trimpath builds (github.com/x/y@v1.2.3/y.go) put the module version in
// the path. The main module of an installed binary is not a dependency.
func (c *classifier) isGoDependency(filename string) bool {
	if !isGoSource(filename) || !goModuleVersion.MatchString(filename) {
		return false
	}
	for _, dir := range c.mainModuleDirs {
		if strings.HasPrefix(filename, dir) {
			return false
		}
	}
	return true
}

// isGoStdlib reports whether a Go function is in the standard library, by
// the first element of its import path. Unlike the file path, this works for
// -trimpath builds and for dotless application modules that sit under a
// src/ directory (e.g. module "app" in /usr/src/app).
func isGoStdlib(name, filename string) bool {
	if !isGoSource(filename) {
		return false
	}
	first, _, _ := strings.Cut(goImportPath(name), "/")
	return goStdTopLevel[first]
}

// goStdTopLevel holds the first import-path element of every standard
// library package (`go list std | cut -d/ -f1 | sort -u`, Go 1.27).
var goStdTopLevel = map[string]bool{
	"archive": true, "bufio": true, "bytes": true, "cmp": true, "compress": true,
	"container": true, "context": true, "crypto": true, "database": true, "debug": true,
	"embed": true, "encoding": true, "errors": true, "expvar": true, "flag": true,
	"fmt": true, "go": true, "hash": true, "html": true, "image": true,
	"index": true, "internal": true, "io": true, "iter": true, "log": true,
	"maps": true, "math": true, "mime": true, "net": true, "os": true,
	"path": true, "plugin": true, "reflect": true, "regexp": true, "runtime": true,
	"slices": true, "sort": true, "strconv": true, "strings": true, "structs": true,
	"sync": true, "syscall": true, "testing": true, "text": true, "time": true,
	"unicode": true, "unique": true, "unsafe": true, "uuid": true, "vendor": true,
	"weak": true,
}

// goImportPath extracts the import path from a Go function name such as
// "github.com/x/y.(*T).M" or "slices.Sort[go.shape.int]".
func goImportPath(name string) string {
	if i := strings.IndexByte(name, '['); i >= 0 {
		name = name[:i]
	}
	slash := strings.LastIndexByte(name, '/')
	dot := strings.IndexByte(name[slash+1:], '.')
	if dot < 0 {
		return ""
	}
	return name[:slash+1+dot]
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func sampleValue(s *parser.Sample, idx int) int64 {
	if idx < len(s.Values) {
		return s.Values[idx]
	}
	return 0
}
