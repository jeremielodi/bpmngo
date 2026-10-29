package models

import (
	"errors"
	"fmt"
	"testing"
)

func TestIncidentTypeOf(t *testing.T) {
	base := errors.New("boom")
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"untagged", base, IncidentUnknown},
		{"tagged", WithIncidentType(IncidentConditionError, base), IncidentConditionError},
		{"wrapped after tagging", fmt.Errorf("gateway g: %w", WithIncidentType(IncidentExtractValueError, base)), IncidentExtractValueError},
		{"innermost tag wins", WithIncidentType(IncidentIOMappingError, fmt.Errorf("x: %w", WithIncidentType(IncidentExtractValueError, base))), IncidentExtractValueError},
	}
	for _, c := range cases {
		if got := IncidentTypeOf(c.err); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
	if WithIncidentType(IncidentConditionError, nil) != nil {
		t.Error("WithIncidentType(nil) must stay nil")
	}
}

func TestLegacyIncidentType(t *testing.T) {
	cases := []struct {
		typ    string
		hasJob bool
		want   string
	}{
		{IncidentJobNoRetries, true, "failedExternalTask"},
		{IncidentUnhandledErrorEvent, true, "failedExternalTask"},
		{IncidentUnhandledErrorEvent, false, "unhandledErrorEvent"},
		{IncidentConditionError, false, "executionError"},
		{IncidentUnknown, false, "executionError"},
		// Rows written before migration 007 keep their name.
		{"failedExternalTask", true, "failedExternalTask"},
		{"executionError", false, "executionError"},
	}
	for _, c := range cases {
		if got := LegacyIncidentType(c.typ, c.hasJob); got != c.want {
			t.Errorf("LegacyIncidentType(%s, %v) = %s, want %s", c.typ, c.hasJob, got, c.want)
		}
	}
}
