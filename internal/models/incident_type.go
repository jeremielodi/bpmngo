package models

import "errors"

// Incident types, as Camunda 8 names them (ErrorType in Operate and the
// /v2 API). The incidents table stores these; the Camunda 7 API shows the
// older names (LegacyIncidentType).
const (
	IncidentJobNoRetries             = "JOB_NO_RETRIES"
	IncidentUnhandledErrorEvent      = "UNHANDLED_ERROR_EVENT"
	IncidentConditionError           = "CONDITION_ERROR"
	IncidentExtractValueError        = "EXTRACT_VALUE_ERROR"
	IncidentIOMappingError           = "IO_MAPPING_ERROR"
	IncidentCalledElementError       = "CALLED_ELEMENT_ERROR"
	IncidentCalledDecisionError      = "CALLED_DECISION_ERROR"
	IncidentDecisionEvaluationError  = "DECISION_EVALUATION_ERROR"
	IncidentUnknown                  = "UNKNOWN"
	legacyIncidentFailedExternalTask = "failedExternalTask"
	legacyIncidentExecutionError     = "executionError"
	legacyIncidentUnhandledError     = "unhandledErrorEvent"
)

// IncidentError is an error that knows which incident it raises.
type IncidentError struct {
	Type string
	Err  error
}

func (e *IncidentError) Error() string { return e.Err.Error() }
func (e *IncidentError) Unwrap() error { return e.Err }

// WithIncidentType tags err with the incident type it raises; nil stays nil.
func WithIncidentType(incidentType string, err error) error {
	if err == nil {
		return nil
	}
	return &IncidentError{Type: incidentType, Err: err}
}

// IncidentTypeOf is the incident type err was tagged with (the innermost
// tag wins), or IncidentUnknown.
func IncidentTypeOf(err error) string {
	t := IncidentUnknown
	for err != nil {
		var ie *IncidentError
		if !errors.As(err, &ie) {
			break
		}
		t = ie.Type
		err = ie.Err
	}
	return t
}

// LegacyIncidentType is the Camunda 7 era name BpmnGo's /engine-rest API
// shows for an incident type: failedExternalTask for a job (out of
// retries, or whose BPMN error nothing caught), unhandledErrorEvent for an
// uncaught error thrown by an event, executionError for everything else.
func LegacyIncidentType(incidentType string, hasJob bool) string {
	switch incidentType {
	case IncidentJobNoRetries, legacyIncidentFailedExternalTask:
		return legacyIncidentFailedExternalTask
	case IncidentUnhandledErrorEvent, legacyIncidentUnhandledError:
		if hasJob {
			return legacyIncidentFailedExternalTask
		}
		return legacyIncidentUnhandledError
	default:
		return legacyIncidentExecutionError
	}
}
