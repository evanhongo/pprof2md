package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"pprof2md/core"
	"pprof2md/core/formatter"
	"pprof2md/core/parser"
)

// Build information, overridable at build time via -ldflags.
var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
	goVersion = "unknown"
)

// errSilent signals that an error was already reported; main just exits non-zero.
var errSilent = errors.New("")

var (
	flagFormat             string
	flagType               string
	flagOutput             string
	flagSourceDir          string
	flagMaxSelfCount       int
	flagMaxCumulativeCount int
)

var validFormats = map[string]bool{"summary": true, "detail": true}
var validTypes = map[string]bool{"cpu": true, "heap": true, "mutex": true, "goroutine": true}

var rootCmd = &cobra.Command{
	Use:   "pprof2md [flags] <profile.pb[.gz]>",
	Short: "Convert pprof profiles to LLM-friendly Markdown",
	Long: `pprof2md converts binary pprof profiles into structured Markdown that LLMs can
analyze to identify performance bottlenecks, explain root causes and suggest
optimizations.

FORMAT LEVELS:
  summary  - Executive summary: the top functions by self and by cumulative
             cost (best for quick triage)
  detail   - Executive summary with a drill-down section and the source of
             each function it ranks (best for investigation)`,
	Example: `  # Basic usage - analyze a CPU profile
  pprof2md cpu-profile.pb.gz

  # Detail format with source files under ./src
  pprof2md --source-dir=./src profile.pb.gz

  # Summary of the top 10 functions by self cost, written to a file
  pprof2md --format=summary --max-self-count=10 profile.pb -o analysis.md

  # Memory profile analysis
  pprof2md --type=heap heap-profile.pb.gz`,
	Args:          cobra.ArbitraryArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	Version:       version,
	RunE:          run,
}

func init() {
	flags := rootCmd.Flags()
	flags.StringVarP(&flagFormat, "format", "f", "detail", "Output format: summary, detail")
	flags.StringVarP(&flagType, "type", "t", "auto", "Profile type: cpu, heap, mutex, goroutine, auto")
	flags.StringVarP(&flagOutput, "output", "o", "", "Output file (default: stdout)")
	flags.StringVarP(&flagSourceDir, "source-dir", "s", "", "Source directory for code context in the detail format (default: current directory)")
	flags.IntVar(&flagMaxSelfCount, "max-self-count", formatter.DefaultMaxCount, "Number of functions to rank by self cost")
	flags.IntVar(&flagMaxCumulativeCount, "max-cumulative-count", formatter.DefaultMaxCount, "Number of functions to rank by cumulative cost")

	rootCmd.SetVersionTemplate(fmt.Sprintf(
		"pprof2md\n  Version:    {{.Version}}\n  Commit:     %s\n  Built:      %s\n  Go version: %s\n",
		commit, buildTime, goVersion))
}

func run(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		cmd.PrintErrln("Error: No profile file specified")
		cmd.PrintErrln("Use --help for usage information")
		return errSilent
	}
	profilePath := args[0]

	if !fileExists(profilePath) {
		cmd.PrintErrln("Error: File not found: " + profilePath)
		return errSilent
	}

	if !validFormats[flagFormat] {
		cmd.PrintErrln("Error: Invalid format: " + flagFormat)
		cmd.PrintErrln("Valid formats: summary, detail")
		return errSilent
	}

	if flagMaxSelfCount < 1 || flagMaxCumulativeCount < 1 {
		cmd.PrintErrln("Error: --max-self-count and --max-cumulative-count must be at least 1")
		return errSilent
	}

	var profileType parser.ProfileType
	if flagType != "auto" {
		if !validTypes[flagType] {
			cmd.PrintErrln("Error: Invalid profile type: " + flagType)
			cmd.PrintErrln("Valid types: cpu, heap, mutex, goroutine, auto")
			return errSilent
		}
		profileType = parser.ProfileType(flagType)
	}

	output, err := core.ConvertFile(profilePath, core.ConvertOptions{
		Format:             formatter.Level(flagFormat),
		ProfileType:        profileType,
		ProfileName:        basename(profilePath),
		SourceDir:          flagSourceDir,
		MaxSelfCount:       flagMaxSelfCount,
		MaxCumulativeCount: flagMaxCumulativeCount,
	})
	if err != nil {
		cmd.PrintErrln("Error: " + err.Error())
		return errSilent
	}

	if flagOutput != "" {
		if err := os.WriteFile(flagOutput, []byte(output), 0o644); err != nil {
			cmd.PrintErrln("Error: " + err.Error())
			return errSilent
		}
		cmd.PrintErrln("Output written to " + flagOutput)
		return nil
	}

	fmt.Fprintln(cmd.OutOrStdout(), output)
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func basename(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		if !errors.Is(err, errSilent) {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(1)
	}
}
