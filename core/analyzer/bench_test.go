package analyzer_test

import (
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"testing"

	"pprof2md/core/analyzer"
	"pprof2md/core/parser"
)

// syntheticProfile builds n samples whose stacks are drawn from a shared pool
// of locations, as in real profiles. goStyle sets StartLine and varies the
// sampled line within each function; otherwise frames have no StartLine and
// one line per function, as V8 exporters write them.
func syntheticProfile(n int, goStyle bool) *parser.Profile {
	r := rand.New(rand.NewPCG(1, 2))
	const funcs, linesPerFunc = 400, 4
	pool := make([]parser.Location, 0, funcs*linesPerFunc)
	for f := range funcs {
		for l := range linesPerFunc {
			fn := parser.FunctionInfo{Name: fmt.Sprintf("pkg%d.f%d", f%20, f), Filename: fmt.Sprintf("/app/pkg%d/f%d.js", f%20, f)}
			line := 10 * f
			if goStyle {
				fn.Filename = fmt.Sprintf("/app/pkg%d/f%d.go", f%20, f)
				fn.StartLine = 10 * f
				line += l
			}
			pool = append(pool, parser.Location{ID: uint64(len(pool) + 1), Lines: []parser.LineInfo{{Line: line, Function: fn}}})
		}
	}
	p := &parser.Profile{SampleTypes: []parser.SampleType{{Type: "samples", Unit: "count"}}}
	for range n {
		depth := 5 + r.IntN(25)
		stack := make([]parser.Location, depth)
		for i := range stack {
			// Bias outer frames toward a few roots so the tree shares prefixes.
			if i >= depth-3 {
				stack[i] = pool[r.IntN(20)]
			} else {
				stack[i] = pool[r.IntN(len(pool))]
			}
		}
		p.Samples = append(p.Samples, parser.Sample{Stack: stack, Values: []int64{1 + r.Int64N(10)}})
	}
	return p
}

func benchmarkAnalyze(b *testing.B, p *parser.Profile) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := analyzer.Analyze(p, analyzer.Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAnalyzeNoStartLine(b *testing.B) { benchmarkAnalyze(b, syntheticProfile(50_000, false)) }
func BenchmarkAnalyzeGo(b *testing.B)          { benchmarkAnalyze(b, syntheticProfile(50_000, true)) }

func BenchmarkAnalyzeEvalProfile(b *testing.B) {
	p, err := parser.ParseFile(filepath.Join("..", "..", "eval", "apps", "memory-churn", "profile.pb.gz"))
	if err != nil {
		b.Skipf("eval profile: %v", err)
	}
	benchmarkAnalyze(b, p)
}
