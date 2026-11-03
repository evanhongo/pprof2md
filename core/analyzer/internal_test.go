package analyzer

import (
	"os/exec"
	"strings"
	"testing"
)

// The standard-library list must cover the Go toolchain in use; a new
// top-level package would otherwise count as application code.
func TestGoStdTopLevelCoversInstalledGo(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go command not found")
	}
	out, err := exec.Command(gobin, "list", "std").Output()
	if err != nil {
		t.Fatalf("go list std: %v", err)
	}
	for pkg := range strings.FieldsSeq(string(out)) {
		first, _, _ := strings.Cut(pkg, "/")
		if !goStdTopLevel[first] {
			t.Errorf("goStdTopLevel lacks %q (from %s); add it", first, pkg)
		}
	}
}

func TestLineCostsWithManyLines(t *testing.T) {
	var c, d lineCosts
	for l := range 500 {
		c.addCum(l, 1)
		c.addSelf(l, int64(l%97))
		d.addSelf(l, 1)
	}
	c.merge(d)
	// Line 96 has 96+1 self; so do 193, 290, 387 and 484, and ties go to the
	// lowest line.
	if line := c.hottest(); line != 96 {
		t.Errorf("hottest = %d, want 96", line)
	}
	if got := c.at(250); got.self != 250%97+1 || got.cum != 1 {
		t.Errorf("line 250 = %+v, want self %d cum 1", *got, 250%97+1)
	}
}

func BenchmarkLineCostsManyLines(b *testing.B) {
	for b.Loop() {
		var c lineCosts
		for i := range 200_000 {
			c.addCum(i%10_000, 1)
		}
	}
}
