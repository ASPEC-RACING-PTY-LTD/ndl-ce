package ai

import (
	"fmt"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/metrics"
)

// Citation is one event or metric the answer used. It is not a model proof.
type Citation struct {
	Kind    string `json:"kind"`
	Ref     string `json:"ref"`
	Summary string `json:"summary"`
}

// Context is read-only infrastructure material for Ask.
type Context struct {
	Events  []Citation
	Metrics []Citation
}

// BuildContext copies events and the last metric point. Secrets are redacted.
func BuildContext(events []appdb.Event, series []metrics.Series, limit int) Context {
	if limit <= 0 {
		limit = 20
	}
	ctx := Context{}
	for i, e := range events {
		if i >= limit {
			break
		}
		summary := Redact(fmt.Sprintf("%s %s", e.Type, strings.TrimSpace(string(e.Payload))))
		ctx.Events = append(ctx.Events, Citation{Kind: "event", Ref: e.Type, Summary: summary})
	}
	for _, s := range series {
		if len(ctx.Metrics) >= limit {
			break
		}
		if len(s.Points) == 0 {
			continue
		}
		p := s.Points[len(s.Points)-1]
		summary := Redact(fmt.Sprintf("%s=%g at %s status %s", s.Name, p.Value, p.Time.UTC().Format(time.RFC3339), s.Status))
		ctx.Metrics = append(ctx.Metrics, Citation{Kind: "metric", Ref: s.Name, Summary: summary})
	}
	return ctx
}

// LocalAnswer cites events and metrics without a vendor model.
func LocalAnswer(question string, ctx Context) string {
	q := strings.ToLower(question)
	var b strings.Builder
	b.WriteString("Local Ask (no vendor required). ")
	if strings.Contains(q, "restart") {
		found := false
		for _, c := range ctx.Events {
			if strings.Contains(strings.ToLower(c.Ref+" "+c.Summary), "restart") {
				b.WriteString("A matching event is ")
				b.WriteString(c.Summary)
				b.WriteString(". ")
				found = true
				break
			}
		}
		if !found {
			b.WriteString("No restart event is in the recent window. ")
		}
	}
	if len(ctx.Events) == 0 && len(ctx.Metrics) == 0 {
		b.WriteString("No events or metrics were available to cite.")
		return Redact(b.String())
	}
	b.WriteString("Cited events: ")
	if len(ctx.Events) == 0 {
		b.WriteString("none. ")
	} else {
		for i, c := range ctx.Events {
			if i > 0 {
				b.WriteString("; ")
			}
			b.WriteString(c.Summary)
		}
		b.WriteString(". ")
	}
	b.WriteString("Cited metrics: ")
	if len(ctx.Metrics) == 0 {
		b.WriteString("none.")
	} else {
		for i, c := range ctx.Metrics {
			if i > 0 {
				b.WriteString("; ")
			}
			b.WriteString(c.Summary)
		}
		b.WriteString(".")
	}
	return Redact(b.String())
}
