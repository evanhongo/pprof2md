// Package core is the library entry point: it wires parsing, analysis and
// formatting into a single Convert call, mirroring the reference convert() API.
package core

import (
	"pprof2md/core/analyzer"
	"pprof2md/core/formatter"
	"pprof2md/core/parser"
)

// ConvertOptions configures a conversion. Zero values select sensible defaults:
// summary format, auto-detected profile type, auto-selected sample type, the
// current directory for source files and formatter.DefaultMaxCount functions
// in each ranking.
type ConvertOptions struct {
	Format             formatter.Level
	ProfileType        parser.ProfileType
	SampleTypeIndex    *int
	ProfileName        string
	SourceDir          string
	MaxSelfCount       int
	MaxCumulativeCount int
}

// Convert parses a pprof profile from a buffer and renders it to Markdown.
func Convert(data []byte, opts ConvertOptions) (string, error) {
	p, err := parser.Parse(data)
	if err != nil {
		return "", err
	}
	return convert(p, opts)
}

// ConvertFile parses a pprof profile from a file and renders it to Markdown.
func ConvertFile(path string, opts ConvertOptions) (string, error) {
	p, err := parser.ParseFile(path)
	if err != nil {
		return "", err
	}
	return convert(p, opts)
}

func convert(p *parser.Profile, opts ConvertOptions) (string, error) {
	pt := opts.ProfileType
	if pt == "" {
		pt = parser.DetectProfileType(p)
	}

	idx := analyzer.GetPrimarySampleTypeIndex(p, pt)
	if opts.SampleTypeIndex != nil {
		idx = *opts.SampleTypeIndex
	}

	result, err := analyzer.Analyze(p, analyzer.Options{SampleTypeIndex: idx})
	if err != nil {
		return "", err
	}

	level := opts.Format
	if level == "" {
		level = formatter.LevelSummary
	}

	return formatter.Format(result, pt, level, formatter.Options{
		ProfileName:        opts.ProfileName,
		SourceDir:          opts.SourceDir,
		MaxSelfCount:       opts.MaxSelfCount,
		MaxCumulativeCount: opts.MaxCumulativeCount,
	})
}
