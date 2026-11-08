package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/simulation"
	"github.com/jmoiron/sqlx"
)

func TestRunScenarioErrorClassification(t *testing.T) {
	const bpmn = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="process" isExecutable="true"><startEvent id="start"/><task id="work" name="Installation"/><endEvent id="end"/><sequenceFlow id="f1" sourceRef="start" targetRef="work"/><sequenceFlow id="f2" sourceRef="work" targetRef="end"/></process></definitions>`
	for _, tc := range []struct {
		name           string
		capacity       int
		invalidArrival bool
		readError      error
		writeError     error
		status         int
		messages       []string
	}{
		{name: "team configuration", capacity: 1, status: 400, messages: []string{"Installation", "capacity 1", "needs 3 people", `missing required skills [\"3\"]`}},
		{name: "arrival configuration", capacity: 3, invalidArrival: true, status: 400, messages: []string{"arrival"}},
		{name: "missing scenario", readError: sql.ErrNoRows, status: 404},
		{name: "database read failure", readError: errors.New("database unavailable"), status: 500},
		{name: "database write failure", capacity: 3, writeError: errors.New("database unavailable"), status: 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			id := uuid.New()
			expect := mock.ExpectQuery("SELECT .* FROM simulation_scenarios WHERE id").WithArgs(id, "")
			if tc.readError != nil {
				expect.WillReturnError(tc.readError)
			} else {
				scenario := simulation.Scenario{Name: "Team test", BPMNXML: bpmn, Replications: 1,
					ArrivalPattern: &simulation.ArrivalPattern{Type: "scheduled", ScheduledMinutes: []float64{0}},
					Resources:      []simulation.Resource{{ID: "team", Name: "Level 2", Capacity: tc.capacity}},
					Activities:     map[string]simulation.ActivityParameters{"work": {ResourceID: "team", ResourceUnits: 3, RequiredSkills: []string{"3"}, ProcessingTime: simulation.Distribution{Type: "fixed", Value: 10}}},
				}
				if tc.capacity >= 3 {
					scenario.Resources[0].Skills = []string{"3"}
				}
				if tc.invalidArrival {
					scenario.ArrivalPattern = &simulation.ArrivalPattern{Type: "unknown"}
				}
				configuration, err := json.Marshal(scenario)
				if err != nil {
					t.Fatal(err)
				}
				expect.WillReturnRows(sqlmock.NewRows([]string{"id", "name", "bpmn_xml", "configuration"}).AddRow(id.String(), scenario.Name, bpmn, configuration))
			}
			if tc.writeError != nil {
				mock.ExpectQuery("INSERT INTO simulation_runs").WillReturnError(tc.writeError)
			}
			app := fiber.New()
			controller := NewSimulationController(sqlx.NewDb(db, "sqlmock"))
			app.Post("/simulation/:id/runs", controller.RunScenario)
			response, err := app.Test(httptest.NewRequest("POST", "/simulation/"+id.String()+"/runs", nil))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != tc.status {
				t.Fatalf("status %d, want %d: %s", response.StatusCode, tc.status, body)
			}
			for _, message := range tc.messages {
				if !strings.Contains(string(body), message) {
					t.Errorf("response %s does not contain %q", body, message)
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
