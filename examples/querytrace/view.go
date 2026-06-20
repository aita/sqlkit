package main

import (
	"embed"
	"html/template"
	"sort"
	"strconv"
	"strings"
	"time"

	querytrace "github.com/aita/sqlkit/querytrace"
)

// detailView is the data the detail page renders: the demo it ran, the result
// the handler wrote, any handler error, and the trace broken out into a counts
// summary, a warnings list, and a timeline.
type detailView struct {
	Demo     demo
	Output   string
	Err      string
	Report   querytrace.Report
	Warnings []warnRow
	Timeline []timelineRow
}

// detailJSON is the JSON form of a finished trace: the demo metadata, the
// handler's output and error, and the full querytrace.Report (counts, events,
// marks, warnings), which is documented as safe to marshal to JSON.
type detailJSON struct {
	Demo   demoMeta          `json:"demo"`
	Output string            `json:"output"`
	Error  string            `json:"error,omitempty"`
	Report querytrace.Report `json:"report"`
}

// demoMeta is the JSON metadata for one demo endpoint.
type demoMeta struct {
	Path             string   `json:"path"`
	Title            string   `json:"title"`
	Description      string   `json:"description"`
	ExpectedWarnings []string `json:"expectedWarnings,omitempty"`
}

func (d demo) meta() demoMeta {
	return demoMeta{Path: d.Path, Title: d.Title, Description: d.Desc, ExpectedWarnings: d.Expect}
}

// warnRow is one rendered warning.
type warnRow struct {
	Code     string
	Severity string
	Message  string
	Caller   string
}

// timelineRow is one rendered point on the trace timeline.
type timelineRow struct {
	N        int
	Kind     string
	Duration string
	Label    string
	Rows     string
}

// buildView turns a finished Report into the detail page's view model.
func buildView(d demo, rep querytrace.Report, output, err string) detailView {
	v := detailView{Demo: d, Output: output, Err: err, Report: rep}

	for _, w := range rep.Warnings {
		row := warnRow{
			Code:     string(w.Code),
			Severity: w.Severity.String(),
			Message:  w.Message,
		}
		if w.Caller != nil {
			row.Caller = shortFile(w.Caller.File) + ":" + strconv.Itoa(w.Caller.Line)
		}
		v.Warnings = append(v.Warnings, row)
	}

	v.Timeline = buildTimeline(rep)
	return v
}

// buildTimeline merges the report's events and marks into a single time-ordered
// list of rendered rows, mirroring querytrace's text timeline.
func buildTimeline(rep querytrace.Report) []timelineRow {
	type item struct {
		at  time.Time
		row timelineRow
	}
	var items []item

	for _, ev := range rep.Events {
		row := timelineRow{Kind: strings.ToUpper(string(ev.Kind))}
		switch ev.Kind {
		case querytrace.EventTx:
			if ev.Tx != nil {
				row.Label = strings.ToUpper(string(ev.Tx.Action))
			}
		case querytrace.EventQuery, querytrace.EventExec:
			row.Duration = ev.Duration.Round(time.Microsecond).String()
			if ev.Query != nil {
				row.Label = ev.Query.Fingerprint
			}
			if ev.Name != "" {
				row.Label = ev.Name + "  " + row.Label
			}
			if ev.Rows != nil {
				state := "leaked (not closed)"
				if ev.Rows.Closed {
					state = "closed"
				}
				row.Rows = strconv.Itoa(ev.Rows.NextCount) + " rows, " + state
			}
		}
		items = append(items, item{at: ev.StartedAt, row: row})
	}
	for _, m := range rep.Marks {
		items = append(items, item{at: m.At, row: timelineRow{Kind: "MARK", Label: m.Name}})
	}

	sort.SliceStable(items, func(i, j int) bool { return items[i].at.Before(items[j].at) })

	rows := make([]timelineRow, len(items))
	for i, it := range items {
		it.row.N = i + 1
		rows[i] = it.row
	}
	return rows
}

// shortFile trims a source path to its last two segments, enough to locate the
// call site without the absolute prefix.
func shortFile(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) <= 2 {
		return path
	}
	return strings.Join(parts[len(parts)-2:], "/")
}

// baseCSS is the stylesheet shared by both pages, injected into each template's
// <style> block through the "css" function.
//
//go:embed templates/base.css
var baseCSS string

// templateFS holds the HTML templates, embedded so the binary is self-contained.
//
//go:embed templates/*.html
var templateFS embed.FS

// templates parses the embedded HTML, exposing the shared stylesheet through a
// "css" function so each page can render it inline.
var templates = template.Must(template.New("querytrace").Funcs(template.FuncMap{
	"css": func() template.CSS { return template.CSS(baseCSS) },
}).ParseFS(templateFS, "templates/*.html"))

var (
	indexTmpl  = templates.Lookup("index.html")
	detailTmpl = templates.Lookup("detail.html")
)
