package formatter

import (
	"fmt"
	"slices"

	"pprof2md/core/analyzer"
	"pprof2md/core/parser"
)

// Summary renders the compact triage format: the executive summary alone.
func Summary(a *analyzer.Result, pt parser.ProfileType, opts Options) string {
	m := &md{}
	writeHeader(m, a, pt, opts)
	newExecutiveSummary(a, opts).write(m, a, pt, false)

	return m.string()
}

// executiveSummary holds the functions that the executive summary ranks: the
// top ones by self cost and the top ones by cumulative cost.
type executiveSummary struct {
	self, cum []analyzer.Hotspot
	// funcs holds each ranked function once, in the order it first shows:
	// the self ranking, then the cumulative one.
	funcs []analyzer.Hotspot
	// labels and ids hold, by function key, the label of each function (see
	// hotspotLabels) and the anchor id of its drill-down section.
	labels, ids map[string]string
}

func newExecutiveSummary(a *analyzer.Result, opts Options) *executiveSummary {
	e := &executiveSummary{
		self: topHotspots(a.Hotspots, maxCount(opts.MaxSelfCount)),
		cum:  topHotspots(a.CumulativeHotspots, maxCount(opts.MaxCumulativeCount)),
	}
	for _, h := range slices.Concat(e.self, e.cum) {
		if !slices.ContainsFunc(e.funcs, func(f analyzer.Hotspot) bool { return f.Key == h.Key }) {
			e.funcs = append(e.funcs, h)
		}
	}

	labels := hotspotLabels(e.funcs)
	ids := drillIDs(labels)
	e.labels = make(map[string]string, len(e.funcs))
	e.ids = make(map[string]string, len(e.funcs))
	for i, h := range e.funcs {
		e.labels[h.Key], e.ids[h.Key] = labels[i], ids[i]
	}
	return e
}

// write renders the executive summary. With links, each function links to
// its drill-down section.
func (e *executiveSummary) write(m *md, a *analyzer.Result, pt parser.ProfileType, links bool) {
	m.p("## Executive Summary")
	m.blank()
	if len(e.funcs) == 0 {
		m.p("*No significant bottlenecks detected.*")
		return
	}

	terms := termsFor(pt, a.SampleType)
	e.writeRanking(m, rankingHeading(terms.self, a.Diff), e.self, terms.noun, links,
		func(h analyzer.Hotspot) float64 { return h.SelfPercent })
	m.blank()
	e.writeRanking(m, rankingHeading(terms.cum, a.Diff), e.cum, terms.noun, links,
		func(h analyzer.Hotspot) float64 { return h.CumulativePercent })
}

// rankingHeading names a ranking by its cost. A diff ranks changes, not
// bottlenecks.
func rankingHeading(cost string, diff bool) string {
	if diff {
		return "Largest " + cost + " changes"
	}
	return "Top " + cost
}

// writeRanking renders one ranking as a numbered list. The first item says
// what the percentages are a share of.
func (e *executiveSummary) writeRanking(m *md, heading string, ranked []analyzer.Hotspot, noun string, links bool, percent func(analyzer.Hotspot) float64) {
	m.p("### " + heading)
	m.blank()
	if len(ranked) == 0 {
		m.p("*No significant hotspots detected.*")
		return
	}
	for i, h := range ranked {
		share := "**" + pct(percent(h)) + "**"
		if i == 0 {
			share += " of " + noun
		}
		line := fmt.Sprintf("%d. `%s` (%s)", i+1, e.labels[h.Key], share)
		if links {
			line += " → [Details](#" + e.ids[h.Key] + ")"
		}
		m.p(line)
	}
}

func topHotspots(hotspots []analyzer.Hotspot, n int) []analyzer.Hotspot {
	if len(hotspots) < n {
		return hotspots
	}
	return hotspots[:n]
}
