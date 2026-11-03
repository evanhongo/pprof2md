package parser_test

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/google/pprof/profile"

	"pprof2md/core/parser"
)

const profilesDir = "../../test/profiles"

func fixture(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(profilesDir, name)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("fixture %s not found; run `npm run generate-profiles` in the reference repo", name)
	}
	return p
}

func TestParseFileMissing(t *testing.T) {
	_, err := parser.ParseFile("/nonexistent/profile.pb")
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected a not-exist error, got %v", err)
	}
}

func TestParseCPUProfile(t *testing.T) {
	p, err := parser.ParseFile(fixture(t, "cpu-test.pb.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.SampleTypes) == 0 {
		t.Error("should have sample types")
	}
	if len(p.Samples) == 0 {
		t.Error("should have samples")
	}
	if len(p.Functions) == 0 {
		t.Error("should have functions")
	}
	if len(p.Locations) == 0 {
		t.Error("should have locations")
	}
}

func TestParseHeapProfile(t *testing.T) {
	p, err := parser.ParseFile(fixture(t, "heap-test.pb.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.SampleTypes) == 0 {
		t.Error("should have sample types")
	}
}

// cpu-test.pb.gz is a V8 time profile (sample/count, wall/nanoseconds).
func TestDetectProfileTypeV8Time(t *testing.T) {
	p, err := parser.ParseFile(fixture(t, "cpu-test.pb.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if got := parser.DetectProfileType(p); got != parser.ProfileCPU {
		t.Errorf("DetectProfileType = %q, want %q", got, parser.ProfileCPU)
	}
}

func TestDetectProfileTypeHeap(t *testing.T) {
	p, err := parser.ParseFile(fixture(t, "heap-test.pb.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if got := parser.DetectProfileType(p); got != parser.ProfileHeap {
		t.Errorf("DetectProfileType = %q, want %q", got, parser.ProfileHeap)
	}
}

// A V8 time profile, whose time sample type is named wall, is a CPU profile.
func TestDetectProfileTypeV8TimeIsCPU(t *testing.T) {
	types := func(pairs ...string) []parser.SampleType {
		var st []parser.SampleType
		for i := 0; i < len(pairs); i += 2 {
			st = append(st, parser.SampleType{Type: pairs[i], Unit: pairs[i+1]})
		}
		return st
	}
	cases := []struct {
		name  string
		types []parser.SampleType
		want  parser.ProfileType
	}{
		{"V8 time", types("sample", "count", "wall", "nanoseconds"), parser.ProfileCPU},
		{"time only", types("wall", "nanoseconds"), parser.ProfileCPU},
		{"V8 time with CPU time", types("sample", "count", "wall", "nanoseconds", "cpu", "nanoseconds"), parser.ProfileCPU},
		{"Go CPU", types("samples", "count", "cpu", "nanoseconds"), parser.ProfileCPU},
	}
	for _, c := range cases {
		if got := parser.DetectProfileType(&parser.Profile{SampleTypes: c.types}); got != c.want {
			t.Errorf("%s: DetectProfileType = %q, want %q", c.name, got, c.want)
		}
	}
}

// Go's mutex and block profiles measure delay in nanoseconds, but are not CPU
// profiles. Go's goroutine profile names its sample type goroutine.
func TestDetectProfileTypeGoMutexBlockAndGoroutine(t *testing.T) {
	contention := []parser.SampleType{{Type: "contentions", Unit: "count"}, {Type: "delay", Unit: "nanoseconds"}}
	cases := []struct {
		name  string
		types []parser.SampleType
		want  parser.ProfileType
	}{
		{"Go mutex or block", contention, parser.ProfileMutex},
		{"Go goroutine", []parser.SampleType{{Type: "goroutine", Unit: "count"}}, parser.ProfileGoroutine},
	}
	for _, c := range cases {
		if got := parser.DetectProfileType(&parser.Profile{SampleTypes: c.types}); got != c.want {
			t.Errorf("%s: DetectProfileType = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestParseKeepsMappings(t *testing.T) {
	m := &profile.Mapping{ID: 1, File: "/root/go/bin/tool", BuildID: "abc", HasFunctions: true}
	fn := &profile.Function{ID: 1, Name: "main.main", Filename: "main.go"}
	src := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "samples", Unit: "count"}},
		Mapping:    []*profile.Mapping{m},
		Function:   []*profile.Function{fn},
		Location:   []*profile.Location{{ID: 1, Mapping: m, Line: []profile.Line{{Function: fn, Line: 3}}}},
	}
	src.Sample = []*profile.Sample{{Location: src.Location, Value: []int64{1}}}
	var buf bytes.Buffer
	if err := src.Write(&buf); err != nil {
		t.Fatal(err)
	}

	p, err := parser.Parse(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	want := []parser.Mapping{{ID: 1, File: "/root/go/bin/tool", BuildID: "abc"}}
	if !slices.Equal(p.Mappings, want) {
		t.Errorf("Mappings = %+v, want %+v", p.Mappings, want)
	}
}
