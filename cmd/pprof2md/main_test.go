package main

import (
	"bytes"
	"strings"
	"testing"
)

const testProfile = "../../test/profiles/cpu-test.pb.gz"

// execute runs the command with args, starting from the flags' defaults.
func execute(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	for _, name := range []string{"format", "type", "output", "source-dir", "max-self-count", "max-cumulative-count"} {
		f := rootCmd.Flags().Lookup(name)
		if err := f.Value.Set(f.DefValue); err != nil {
			t.Fatalf("reset --%s: %v", name, err)
		}
	}
	var out, errOut bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	rootCmd.SetArgs(args)
	err = rootCmd.Execute()
	return out.String(), errOut.String(), err
}

func TestDefaultFormatIsDetail(t *testing.T) {
	out, stderr, err := execute(t, testProfile)
	if err != nil {
		t.Fatalf("err = %v, stderr = %s", err, stderr)
	}
	for _, want := range []string{"## Executive Summary", "[Details](#", "## Detailed Analysis", "**Cumulative-time:**"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestSummaryFormatIsTheExecutiveSummary(t *testing.T) {
	out, stderr, err := execute(t, "--format=summary", testProfile)
	if err != nil {
		t.Fatalf("err = %v, stderr = %s", err, stderr)
	}
	if !strings.Contains(out, "## Executive Summary") || strings.Contains(out, "## Detailed Analysis") {
		t.Errorf("summary output is not the executive summary alone:\n%s", out)
	}
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

func TestMaxCountFlags(t *testing.T) {
	out, stderr, err := execute(t, "--format=summary", "--max-self-count=1", "--max-cumulative-count=2", testProfile)
	if err != nil {
		t.Fatalf("err = %v, stderr = %s", err, stderr)
	}
	self, cum, ok := strings.Cut(out, "### Top cumulative-time")
	if !ok {
		t.Fatalf("no cumulative ranking in:\n%s", out)
	}
	if got := countItems(self); got != 1 {
		t.Errorf("--max-self-count=1 ranks %d functions", got)
	}
	if got := countItems(cum); got != 2 {
		t.Errorf("--max-cumulative-count=2 ranks %d functions", got)
	}
}

func TestInvalidArgumentsAreRejected(t *testing.T) {
	for _, args := range [][]string{
		{"--format=adaptive"},
		{"--format=detailed"},
		{"--no-source"},
		{"--max-hotspots=3"},
		{"--max-self-count=0"},
		{"--max-cumulative-count=-1"},
	} {
		out, _, err := execute(t, append(args, testProfile)...)
		if err == nil {
			t.Errorf("%v: no error", args)
		}
		if out != "" {
			t.Errorf("%v: wrote output", args)
		}
	}
}
