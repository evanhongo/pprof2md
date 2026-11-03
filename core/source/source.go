// Package source resolves source-code snippets for profile locations so that
// formatters can inline the hot lines of application code.
package source

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Context is a snippet of source around a target line.
type Context struct {
	Filename  string
	StartLine int
	EndLine   int
	Lines     []string
}

// Request identifies a single source location to resolve.
type Request struct {
	Filename string
	Line     int
}

// DefaultContextLines is the number of lines shown on each side of the target.
const DefaultContextLines = 3

// ResolveMultiple resolves many locations at once, reading each distinct file
// only once and reading the files concurrently. The result is keyed by
// "filename:line".
func ResolveMultiple(reqs []Request, sourceDir string, contextLines int) map[string]*Context {
	results := make(map[string]*Context)

	// Collect the distinct filenames that still need reading.
	unique := make(map[string]struct{})
	for _, r := range reqs {
		if r.Filename != "" {
			unique[r.Filename] = struct{}{}
		}
	}

	// Read the distinct files concurrently.
	fileLines := make(map[string][]string)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for filename := range unique {
		wg.Add(1)
		go func(filename string) {
			defer wg.Done()
			path := findSourceFile(filename, sourceDir)
			if path == "" {
				return
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return
			}
			lines := strings.Split(string(data), "\n")
			mu.Lock()
			fileLines[filename] = lines
			mu.Unlock()
		}(filename)
	}
	wg.Wait()

	for _, r := range reqs {
		key := fmt.Sprintf("%s:%d", r.Filename, r.Line)
		if _, done := results[key]; done || r.Filename == "" {
			continue
		}
		lines, ok := fileLines[r.Filename]
		if !ok {
			continue
		}
		results[key] = sliceContext(r.Filename, lines, r.Line, contextLines)
	}

	return results
}

func sliceContext(filename string, allLines []string, line, contextLines int) *Context {
	start := line - contextLines
	if start < 1 {
		start = 1
	}
	end := line + contextLines
	if end > len(allLines) {
		end = len(allLines)
	}
	if start > end {
		start = end
	}
	extracted := make([]string, 0, end-start+1)
	if start-1 < len(allLines) {
		extracted = append(extracted, allLines[start-1:end]...)
	}
	return &Context{
		Filename:  filename,
		StartLine: start,
		EndLine:   end,
		Lines:     extracted,
	}
}

// findSourceFile locates a profile filename under sourceDir using the same
// fallback strategy as the reference implementation.
func findSourceFile(filename, sourceDir string) string {
	if filename == "" {
		return ""
	}

	if filepath.IsAbs(filename) {
		if fileExists(filename) {
			return filename
		}
		// Try stripping leading directories to find a relative match.
		parts := strings.Split(filename, "/")
		for i := range parts {
			rel := strings.Join(parts[i:], "/")
			candidate := filepath.Join(sourceDir, rel)
			if fileExists(candidate) {
				return candidate
			}
		}
	}

	if direct := filepath.Join(sourceDir, filename); fileExists(direct) {
		return direct
	}

	for _, prefix := range []string{"src/", "lib/", "app/", ""} {
		candidate := filepath.Join(sourceDir, prefix, filename)
		if fileExists(candidate) {
			return candidate
		}
	}

	if idx := strings.Index(filename, "node_modules/"); idx >= 0 {
		candidate := filepath.Join(sourceDir, filename[idx:])
		if fileExists(candidate) {
			return candidate
		}
	}

	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
