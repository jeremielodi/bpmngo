package simulation

import (
	"fmt"
	"math"
	"sort"
	"time"
)

type calendarPlan struct {
	calendar   BusinessCalendar
	start      time.Time
	location   *time.Location
	base       int
	exceptions map[string][]CalendarShift
}

func compileCalendars(s Scenario, resources map[string]Resource) (map[string]*calendarPlan, error) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	if s.StartTime != "" {
		parsed, err := time.Parse(time.RFC3339, s.StartTime)
		if err != nil {
			return nil, fmt.Errorf("startTime must be RFC3339: %w", err)
		}
		start = parsed
	}
	calendars := make(map[string]BusinessCalendar, len(s.Calendars))
	for _, calendar := range s.Calendars {
		if calendar.ID == "" {
			return nil, fmt.Errorf("calendar id is required")
		}
		if _, duplicate := calendars[calendar.ID]; duplicate {
			return nil, fmt.Errorf("duplicate calendar %q", calendar.ID)
		}
		location := time.UTC
		if calendar.Timezone != "" {
			var err error
			location, err = time.LoadLocation(calendar.Timezone)
			if err != nil {
				return nil, fmt.Errorf("calendar %q timezone: %w", calendar.ID, err)
			}
		}
		if err := validateShifts(calendar.ID, calendar.Shifts, true); err != nil {
			return nil, err
		}
		seenDates := map[string]bool{}
		for _, exception := range calendar.Exceptions {
			if seenDates[exception.Date] {
				return nil, fmt.Errorf("calendar %q has duplicate exception date %q", calendar.ID, exception.Date)
			}
			seenDates[exception.Date] = true
			if _, err := time.ParseInLocation("2006-01-02", exception.Date, location); err != nil {
				return nil, fmt.Errorf("calendar %q exception date %q is invalid", calendar.ID, exception.Date)
			}
			if err := validateShifts(calendar.ID, exception.Shifts, false); err != nil {
				return nil, err
			}
		}
		calendars[calendar.ID] = calendar
	}
	plans := map[string]*calendarPlan{}
	for id, resource := range resources {
		if resource.CalendarID == "" {
			continue
		}
		calendar, ok := calendars[resource.CalendarID]
		if !ok {
			return nil, fmt.Errorf("resource %q references unknown calendar %q", id, resource.CalendarID)
		}
		location := time.UTC
		if calendar.Timezone != "" {
			location, _ = time.LoadLocation(calendar.Timezone)
		}
		exceptions := make(map[string][]CalendarShift, len(calendar.Exceptions))
		for _, exception := range calendar.Exceptions {
			exceptions[exception.Date] = exception.Shifts
		}
		plans[id] = &calendarPlan{calendar: calendar, start: start, location: location, base: resource.Capacity, exceptions: exceptions}
	}
	return plans, nil
}

func validateShifts(calendarID string, shifts []CalendarShift, requireWeekdays bool) error {
	for _, shift := range shifts {
		if shift.StartMinute < 0 || shift.StartMinute >= 1440 || shift.EndMinute <= shift.StartMinute || shift.EndMinute > 1440 || shift.Capacity < 0 {
			return fmt.Errorf("calendar %q has invalid shift %d..%d", calendarID, shift.StartMinute, shift.EndMinute)
		}
		if requireWeekdays && len(shift.Weekdays) == 0 {
			return fmt.Errorf("calendar %q recurring shift needs weekdays", calendarID)
		}
		for _, weekday := range shift.Weekdays {
			if weekday < 1 || weekday > 7 {
				return fmt.Errorf("calendar %q weekday must be between 1 and 7", calendarID)
			}
		}
	}
	return nil
}

func (p *calendarPlan) capacityAt(at float64) int {
	local := p.start.Add(time.Duration(at * float64(time.Minute))).In(p.location)
	date := local.Format("2006-01-02")
	shifts, exceptional := p.exceptions[date]
	if !exceptional {
		shifts = p.calendar.Shifts
	}
	minute := local.Hour()*60 + local.Minute()
	isoWeekday := int(local.Weekday())
	if isoWeekday == 0 {
		isoWeekday = 7
	}
	capacity := 0
	for _, shift := range shifts {
		if !exceptional && !containsWeekday(shift.Weekdays, isoWeekday) {
			continue
		}
		if minute < shift.StartMinute || minute >= shift.EndMinute {
			continue
		}
		value := shift.Capacity
		if value == 0 || value > p.base {
			value = p.base
		}
		if value > capacity {
			capacity = value
		}
	}
	return capacity
}

func containsWeekday(values []int, value int) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// nextChange uses minute boundaries because calendar input has minute
// precision. It searches two weeks, enough to cover any valid weekly shift.
func (p *calendarPlan) nextChange(at float64) (float64, bool) {
	current := p.capacityAt(at)
	candidate := math.Floor(at) + 1
	for range 14 * 24 * 60 {
		if p.capacityAt(candidate) != current {
			return candidate, true
		}
		candidate++
	}
	return 0, false
}

func (p *calendarPlan) nextAvailable(at float64) (float64, bool) {
	if p.capacityAt(at) > 0 {
		return at, true
	}
	for range 14 * 24 * 60 {
		next, ok := p.nextChange(at)
		if !ok {
			return 0, false
		}
		at = next
		if p.capacityAt(at) > 0 {
			return at, true
		}
	}
	return 0, false
}

func (p *calendarPlan) completionAt(start, workMinutes float64) (float64, error) {
	at, ok := p.nextAvailable(start)
	if !ok {
		return 0, fmt.Errorf("calendar %q has no availability within two weeks", p.calendar.ID)
	}
	remaining := workMinutes
	for remaining > 0 {
		if p.capacityAt(at) == 0 {
			at, ok = p.nextAvailable(at)
			if !ok {
				return 0, fmt.Errorf("calendar %q has no future availability", p.calendar.ID)
			}
		}
		next, hasChange := p.nextChange(at)
		if !hasChange || remaining <= next-at {
			return at + remaining, nil
		}
		remaining -= next - at
		at = next
	}
	return at, nil
}

func (p *calendarPlan) capacityMinutesBetween(start, end float64) float64 {
	total := 0.0
	for start < end {
		capacity := p.capacityAt(start)
		next, ok := p.nextChange(start)
		if !ok || next > end {
			next = end
		}
		total += (next - start) * float64(capacity)
		start = next
	}
	return total
}

func sortedSkills(skills []string) []string {
	result := append([]string(nil), skills...)
	sort.Strings(result)
	return result
}
