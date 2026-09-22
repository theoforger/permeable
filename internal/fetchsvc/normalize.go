package fetchsvc

import (
	"errors"
	"fmt"

	ics "github.com/arran4/golang-ical"

	"permeable/internal/model"
)

// normalizeEvent converts one parsed VEVENT into a model.Event. It does
// not expand RRULE occurrences (Stage 4) — RRule/ExDates are carried
// through raw for that step.
func normalizeEvent(sourceID int64, ev *ics.VEvent) (model.Event, error) {
	uid := ev.Id()
	if uid == "" {
		return model.Event{}, errors.New("VEVENT missing UID")
	}

	e := model.Event{SourceID: sourceID, UID: uid}

	if p := ev.GetProperty(ics.ComponentPropertySummary); p != nil {
		e.Title = ics.FromText(p.Value)
	}
	if p := ev.GetProperty(ics.ComponentPropertyDescription); p != nil {
		e.Description = ics.FromText(p.Value)
	}
	if p := ev.GetProperty(ics.ComponentPropertyLocation); p != nil {
		e.Location = ics.FromText(p.Value)
	}

	e.AllDay = isAllDay(ev)

	var err error
	if e.AllDay {
		e.Start, err = ev.GetAllDayStartAt()
		if err != nil {
			return model.Event{}, fmt.Errorf("all-day start time: %w", err)
		}
		if e.End, err = ev.GetAllDayEndAt(); err != nil {
			// DTEND is optional on all-day events; RFC 5545 default is one day.
			e.End = e.Start.AddDate(0, 0, 1)
		}
	} else {
		e.Start, err = ev.GetStartAt()
		if err != nil {
			return model.Event{}, fmt.Errorf("start time: %w", err)
		}
		if e.End, err = ev.GetEndAt(); err != nil {
			// DTEND (or DURATION, not yet supported) missing; treat as zero-length.
			e.End = e.Start
		}
	}

	if p := ev.GetProperty(ics.ComponentPropertyRrule); p != nil {
		e.RRule = p.Value
	}
	if exdates, err := ev.GetExDates(); err == nil {
		e.ExDates = exdates
	}

	return e, nil
}

// isAllDay reports whether DTSTART carries VALUE=DATE (an all-day event)
// rather than a date-time.
func isAllDay(ev *ics.VEvent) bool {
	p := ev.GetProperty(ics.ComponentPropertyDtStart)
	if p == nil {
		return false
	}
	for _, v := range p.ICalParameters["VALUE"] {
		if v == "DATE" {
			return true
		}
	}
	return false
}
