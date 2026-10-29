package simulation

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
)

func validateArrivalPattern(s Scenario) error {
	if s.ArrivalPattern == nil || strings.TrimSpace(s.ArrivalPattern.Type) == "" {
		if s.ArrivalRatePerHour <= 0 {
			return fmt.Errorf("arrivalRatePerHour must be positive")
		}
		return nil
	}
	pattern := s.ArrivalPattern
	switch strings.ToLower(pattern.Type) {
	case "poisson", "batch_poisson":
		rate := pattern.RatePerHour
		if rate <= 0 {
			rate = s.ArrivalRatePerHour
		}
		if rate <= 0 {
			return fmt.Errorf("arrival pattern ratePerHour must be positive")
		}
		if strings.ToLower(pattern.Type) == "batch_poisson" && pattern.BatchSize < 1 {
			return fmt.Errorf("batch_poisson batchSize must be positive")
		}
	case "piecewise_poisson":
		if len(pattern.Segments) == 0 {
			return fmt.Errorf("piecewise_poisson requires at least one segment")
		}
		segments := append([]ArrivalSegment(nil), pattern.Segments...)
		sort.Slice(segments, func(i, j int) bool { return segments[i].StartMinute < segments[j].StartMinute })
		lastEnd := -1.0
		for _, segment := range segments {
			if segment.StartMinute < 0 || segment.EndMinute <= segment.StartMinute || segment.RatePerHour < 0 {
				return fmt.Errorf("invalid arrival segment %.2f..%.2f at rate %.2f", segment.StartMinute, segment.EndMinute, segment.RatePerHour)
			}
			if segment.StartMinute < lastEnd {
				return fmt.Errorf("arrival segments must not overlap")
			}
			lastEnd = segment.EndMinute
		}
	case "scheduled":
		if len(pattern.ScheduledMinutes) == 0 {
			return fmt.Errorf("scheduled arrival pattern requires scheduledMinutes")
		}
		for _, minute := range pattern.ScheduledMinutes {
			if minute < 0 {
				return fmt.Errorf("scheduled arrival minute must be non-negative")
			}
		}
	default:
		return fmt.Errorf("unsupported arrival pattern %q", pattern.Type)
	}
	return nil
}

func hasFiniteArrivalPattern(s Scenario) bool {
	if s.ArrivalPattern == nil {
		return false
	}
	typeName := strings.ToLower(s.ArrivalPattern.Type)
	return typeName == "scheduled" || typeName == "piecewise_poisson"
}

func arrivalEstimate(s Scenario) float64 {
	if s.ArrivalPattern == nil || s.ArrivalPattern.Type == "" {
		return s.DurationHours * s.ArrivalRatePerHour
	}
	pattern := s.ArrivalPattern
	switch strings.ToLower(pattern.Type) {
	case "poisson":
		rate := pattern.RatePerHour
		if rate <= 0 {
			rate = s.ArrivalRatePerHour
		}
		return s.DurationHours * rate
	case "batch_poisson":
		rate := pattern.RatePerHour
		if rate <= 0 {
			rate = s.ArrivalRatePerHour
		}
		return s.DurationHours * rate * float64(pattern.BatchSize)
	case "piecewise_poisson":
		total := 0.0
		for _, segment := range pattern.Segments {
			end := segment.EndMinute
			if s.DurationHours > 0 && end > s.DurationHours*60 {
				end = s.DurationHours * 60
			}
			if end > segment.StartMinute {
				total += (end - segment.StartMinute) / 60 * segment.RatePerHour
			}
		}
		return total
	case "scheduled":
		return float64(len(pattern.ScheduledMinutes))
	default:
		return 0
	}
}

func scheduleArrivals(s Scenario, rng *rand.Rand) []float64 {
	patternType := "poisson"
	if s.ArrivalPattern != nil && s.ArrivalPattern.Type != "" {
		patternType = strings.ToLower(s.ArrivalPattern.Type)
	}
	horizon := s.DurationHours * 60
	limit := s.MaxArrivals
	arrivals := make([]float64, 0)
	appendArrival := func(at float64, batch int) bool {
		if horizon > 0 && at > horizon {
			return false
		}
		if batch < 1 {
			batch = 1
		}
		for range batch {
			if limit > 0 && len(arrivals) >= limit {
				return false
			}
			arrivals = append(arrivals, at)
		}
		return limit <= 0 || len(arrivals) < limit
	}
	poisson := func(start, end, rate float64, batch int) {
		if rate <= 0 {
			return
		}
		at := start + rng.ExpFloat64()/(rate/60)
		for (end <= 0 || at <= end) && (limit <= 0 || len(arrivals) < limit) {
			if !appendArrival(at, batch) {
				break
			}
			at += rng.ExpFloat64() / (rate / 60)
		}
	}
	switch patternType {
	case "scheduled":
		minutes := append([]float64(nil), s.ArrivalPattern.ScheduledMinutes...)
		sort.Float64s(minutes)
		for _, at := range minutes {
			if !appendArrival(at, 1) {
				break
			}
		}
	case "piecewise_poisson":
		segments := append([]ArrivalSegment(nil), s.ArrivalPattern.Segments...)
		sort.Slice(segments, func(i, j int) bool { return segments[i].StartMinute < segments[j].StartMinute })
		for _, segment := range segments {
			end := segment.EndMinute
			if horizon > 0 && end > horizon {
				end = horizon
			}
			if end > segment.StartMinute {
				poisson(segment.StartMinute, end, segment.RatePerHour, 1)
			}
		}
	case "batch_poisson":
		rate := s.ArrivalPattern.RatePerHour
		if rate <= 0 {
			rate = s.ArrivalRatePerHour
		}
		poisson(0, horizon, rate, s.ArrivalPattern.BatchSize)
	default:
		rate := s.ArrivalRatePerHour
		if s.ArrivalPattern != nil && s.ArrivalPattern.RatePerHour > 0 {
			rate = s.ArrivalPattern.RatePerHour
		}
		poisson(0, horizon, rate, 1)
	}
	return arrivals
}
