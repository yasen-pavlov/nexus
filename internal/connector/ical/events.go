package ical

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/muty/nexus/internal/model"
)

// loc is the default location used to resolve floating times and expand
// recurrences. Concrete TZID-bearing values carry their own zone via
// VTIMEZONE; UTC is the deterministic default for the rest.
var loc = time.UTC

// eventsToDocuments turns one .ics resource (identified by its href) into
// series-level documents: one for the master series (or one-off event) plus one
// per modified instance (override carrying RECURRENCE-ID). Events whose relevant
// date falls entirely before sync_since are skipped.
func (c *Connector) eventsToDocuments(cal *ical.Calendar, href, calPath, calName string) []model.Document {
	if cal == nil {
		return nil
	}
	events := cal.Events()

	var masters, overrides []ical.Event
	for i := range events {
		if events[i].Props.Get(ical.PropRecurrenceID) != nil {
			overrides = append(overrides, events[i])
		} else {
			masters = append(masters, events[i])
		}
	}

	var docs []model.Document
	for i := range masters {
		if doc, ok := c.masterDocument(masters[i], href, calPath, calName); ok {
			docs = append(docs, doc)
		}
	}
	for i := range overrides {
		if doc, ok := c.overrideDocument(overrides[i], href, calPath, calName); ok {
			docs = append(docs, doc)
		}
	}
	return docs
}

// masterDocument builds the series-level document for a master VEVENT. ok is
// false when the event is entirely before sync_since.
func (c *Connector) masterDocument(ev ical.Event, href, calPath, calName string) (model.Document, bool) {
	dtstart := eventStart(ev)
	recurring := isRecurring(ev)

	var anchor, next, recent time.Time
	if recurring {
		// RecurrenceSet can fail on the same non-IANA TZIDs eventStart tolerates;
		// when it does we just skip occurrence math and anchor to dtstart.
		if set, err := ev.RecurrenceSet(loc); err == nil && set != nil {
			next = set.After(c.now(), true)
			recent = set.Before(c.now(), true)
			if !c.syncSince.IsZero() {
				if first := set.After(c.syncSince.Add(-time.Second), true); first.IsZero() {
					return model.Document{}, false // no occurrence within window
				}
			}
		}
		anchor = nearest(c.now(), next, recent)
		if anchor.IsZero() {
			anchor = dtstart
		}
	} else {
		if !c.syncSince.IsZero() && !dtstart.IsZero() && dtstart.Before(c.syncSince) {
			return model.Document{}, false
		}
		anchor = dtstart
	}

	meta := c.eventMetadata(ev, calName, calPath, recurring, next, recent)
	return c.buildDocument(sourceID(href, ""), ev, anchor, meta), true
}

// overrideDocument builds a document for a modified single instance of a
// recurring series (it carries its own RECURRENCE-ID and distinct content).
func (c *Connector) overrideDocument(ev ical.Event, href, calPath, calName string) (model.Document, bool) {
	dtstart := eventStart(ev)
	if !c.syncSince.IsZero() && !dtstart.IsZero() && dtstart.Before(c.syncSince) {
		return model.Document{}, false
	}
	rid := ""
	if p := ev.Props.Get(ical.PropRecurrenceID); p != nil {
		rid = p.Value
	}
	meta := c.eventMetadata(ev, calName, calPath, false, time.Time{}, time.Time{})
	meta["override_of"] = rid
	return c.buildDocument(sourceID(href, rid), ev, dtstart, meta), true
}

// eventStart / eventEnd return the event's start/end, tolerant of non-IANA
// TZIDs (e.g. Microsoft "W. Europe Standard Time") that go-ical's typed getters
// hard-reject. On failure they fall back to parsing the raw value as a naive
// timestamp — an approximate (UTC-assumed) time is far better for recency and
// search than no date at all.
func eventStart(ev ical.Event) time.Time { return eventTime(ev, ical.PropDateTimeStart) }
func eventEnd(ev ical.Event) time.Time   { return eventTime(ev, ical.PropDateTimeEnd) }

func eventTime(ev ical.Event, prop string) time.Time {
	if t, err := ev.Props.DateTime(prop, loc); err == nil && !t.IsZero() {
		return t
	}
	p := ev.Props.Get(prop)
	if p == nil {
		return time.Time{}
	}
	for _, f := range []string{"20060102T150405Z", "20060102T150405", "20060102"} {
		if t, err := time.Parse(f, p.Value); err == nil {
			return t
		}
	}
	return time.Time{}
}

// buildDocument assembles a model.Document from an event's fields. CreatedAt is
// the recency anchor (the occurrence nearest "now" for series, the start for
// one-offs) so upcoming and recent events rank well.
func (c *Connector) buildDocument(sid string, ev ical.Event, anchor time.Time, meta map[string]any) model.Document {
	summary, _ := ev.Props.Text(ical.PropSummary)
	if summary == "" {
		summary = "(untitled event)"
	}
	return model.Document{
		ID:         model.DocumentID("ical", c.name, sid),
		SourceType: "ical",
		SourceName: c.name,
		SourceID:   sid,
		Title:      summary,
		Content:    eventContent(ev, meta),
		MimeType:   "text/calendar",
		Metadata:   meta,
		Visibility: "private",
		CreatedAt:  anchor,
	}
}

// eventMetadata collects the structured fields used for filtering/snippets.
func (c *Connector) eventMetadata(ev ical.Event, calName, calPath string, recurring bool, next, recent time.Time) map[string]any {
	meta := map[string]any{
		"calendar":      calName,
		"calendar_path": calPath,
		"recurring":     recurring,
	}
	if uid, _ := ev.Props.Text(ical.PropUID); uid != "" {
		meta["uid"] = uid
	}
	if loc, _ := ev.Props.Text(ical.PropLocation); loc != "" {
		meta["location"] = loc
	}
	if org := organizer(ev); org != "" {
		meta["organizer"] = org
	}
	if att := attendees(ev); len(att) > 0 {
		meta["attendees"] = att
	}
	if status, err := ev.Status(); err == nil && status != "" {
		meta["status"] = string(status)
	}
	if start := eventStart(ev); !start.IsZero() {
		meta["dtstart"] = start.Format(time.RFC3339)
	}
	if end := eventEnd(ev); !end.IsZero() {
		meta["dtend"] = end.Format(time.RFC3339)
	}
	if recurring {
		if p := ev.Props.Get(ical.PropRecurrenceRule); p != nil && p.Value != "" {
			meta["rrule"] = p.Value
		}
		if !next.IsZero() {
			meta["next_occurrence"] = next.Format(time.RFC3339)
		}
		if !recent.IsZero() {
			meta["recent_occurrence"] = recent.Format(time.RFC3339)
		}
	}
	return meta
}

// eventContent renders a readable block for full-text + embedding: the
// description followed by when/where/who lines so the event is self-describing
// to a retrieval model.
func eventContent(ev ical.Event, meta map[string]any) string {
	var b strings.Builder
	if desc, _ := ev.Props.Text(ical.PropDescription); desc != "" {
		b.WriteString(desc)
		b.WriteString("\n\n")
	}
	if when := whenLine(meta); when != "" {
		fmt.Fprintf(&b, "When: %s\n", when)
	}
	if loc, ok := meta["location"].(string); ok && loc != "" {
		fmt.Fprintf(&b, "Where: %s\n", loc)
	}
	if org, ok := meta["organizer"].(string); ok && org != "" {
		fmt.Fprintf(&b, "Organizer: %s\n", org)
	}
	if att, ok := meta["attendees"].([]string); ok && len(att) > 0 {
		fmt.Fprintf(&b, "Attendees: %s\n", strings.Join(att, ", "))
	}
	return strings.TrimSpace(b.String())
}

func whenLine(meta map[string]any) string {
	start, _ := meta["dtstart"].(string)
	if recurring, _ := meta["recurring"].(bool); recurring {
		rule, _ := meta["rrule"].(string)
		next, _ := meta["next_occurrence"].(string)
		parts := []string{}
		if start != "" {
			parts = append(parts, "from "+start)
		}
		if rule != "" {
			parts = append(parts, "repeats "+rule)
		}
		if next != "" {
			parts = append(parts, "next "+next)
		}
		return strings.Join(parts, "; ")
	}
	end, _ := meta["dtend"].(string)
	if start != "" && end != "" {
		return start + " – " + end
	}
	return start
}

// isRecurring reports whether an event defines a recurrence (RRULE or RDATE).
func isRecurring(ev ical.Event) bool {
	if rule, err := ev.Props.RecurrenceRule(); err == nil && rule != nil {
		return true
	}
	return ev.Props.Get(ical.PropRecurrenceDates) != nil
}

func organizer(ev ical.Event) string {
	p := ev.Props.Get(ical.PropOrganizer)
	if p == nil {
		return ""
	}
	if cn := p.Params.Get("CN"); cn != "" {
		return cn
	}
	return strings.TrimPrefix(p.Value, "mailto:")
}

func attendees(ev ical.Event) []string {
	props := ev.Props.Values(ical.PropAttendee)
	out := make([]string, 0, len(props))
	for i := range props {
		if cn := props[i].Params.Get("CN"); cn != "" {
			out = append(out, cn)
			continue
		}
		out = append(out, strings.TrimPrefix(props[i].Value, "mailto:"))
	}
	return out
}

// nearest returns whichever of next/recent is closest to now (ignoring zero
// values).
func nearest(now, next, recent time.Time) time.Time {
	switch {
	case next.IsZero():
		return recent
	case recent.IsZero():
		return next
	case next.Sub(now) < now.Sub(recent):
		return next
	default:
		return recent
	}
}

// sourceID is the stable, unique id for an event document: the resource href
// (one per .ics, unique within the account). An override appends
// ":"+RECURRENCE-ID so the pipeline's colon-suffix rule treats it as a child of
// the master — enumerating the master href alone preserves its overrides, which
// lets deletion reconciliation work off the calendar LISTING (every existing
// href) rather than off successfully-fetched content. iCloud hrefs are
// percent-encoded paths and never contain a literal ':', so the suffix is
// unambiguous.
func sourceID(href, recurrenceID string) string {
	if recurrenceID != "" {
		return href + ":" + recurrenceID
	}
	return href
}

// calShort is a short human label for a calendar path (its last segment).
func calShort(calPath string) string {
	trimmed := strings.Trim(calPath, "/")
	if i := strings.LastIndex(trimmed, "/"); i >= 0 {
		return trimmed[i+1:]
	}
	return trimmed
}

func strPtr(s string) *string { return &s }

// --- cursor / manifest -----------------------------------------------------

// manifest maps calendar path → resource href → ETag. The ETag drives the
// incremental diff (skip re-fetching unchanged resources). SourceID enumeration
// for deletion comes from the live listing, not from here.
type manifest map[string]map[string]string

func loadManifest(cursor *model.SyncCursor) manifest {
	if cursor == nil {
		return manifest{}
	}
	raw, ok := cursor.CursorData["manifest"].(string)
	if !ok || raw == "" {
		return manifest{}
	}
	var m manifest
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return manifest{}
	}
	return m
}

func (c *Connector) syncSinceKey() string {
	if c.syncSince.IsZero() {
		return ""
	}
	return c.syncSince.Format("2006-01-02")
}

// newCursor builds a checkpoint cursor. When m is nil it carries only the
// timestamp (a mid-run progress checkpoint); a non-nil m persists the manifest.
func (c *Connector) newCursor(m manifest) *model.SyncCursor {
	now := c.now()
	data := map[string]any{"sync_since": c.syncSinceKey()}
	if m != nil {
		if encoded, err := json.Marshal(m); err == nil {
			data["manifest"] = string(encoded)
		}
	}
	return &model.SyncCursor{
		CursorData: data,
		LastSync:   now,
		LastStatus: "success",
	}
}

func cursorString(cursor *model.SyncCursor, key string) string {
	if cursor == nil {
		return ""
	}
	s, _ := cursor.CursorData[key].(string)
	return s
}

// emitItem sends an item respecting context cancellation; false means the
// context was cancelled before the send completed.
func emitItem(ctx context.Context, items chan<- model.FetchItem, item model.FetchItem) bool {
	select {
	case items <- item:
		return true
	case <-ctx.Done():
		return false
	}
}
