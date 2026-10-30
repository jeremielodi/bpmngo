package v2

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/repository"
	"github.com/jmoiron/sqlx"
)

func TestRestIntKeysMode(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", true}, {"int", true}, {" INT ", true}, {"uuid", false}, {"other", true},
	} {
		if got := restIntKeysMode(tc.value); got != tc.want {
			t.Errorf("restIntKeysMode(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

func mockKeys(t *testing.T) (*repository.ZeebeKeyRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	mock.MatchExpectationsInOrder(false)
	t.Cleanup(func() { db.Close() })
	return repository.NewZeebeKeyRepository(sqlx.NewDb(db, "postgres")), mock
}

func TestIntKeysInJSON(t *testing.T) {
	keys, mock := mockKeys(t)
	inst, def := uuid.New(), uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT engine_key FROM public.process_instances WHERE id = $1")).WithArgs(inst).
		WillReturnRows(sqlmock.NewRows([]string{"engine_key"}).AddRow(125))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT engine_key FROM public.process_definitions WHERE id = $1")).WithArgs(def).
		WillReturnRows(sqlmock.NewRows([]string{"engine_key"}).AddRow(126))
	body := `{"items":[{"processInstanceKey":"` + inst.String() + `","processDefinitionKey":"` + def.String() +
		`","userTaskKey":"127","elementId":"review",` +
		`"variables":{"processInstanceKey":"` + inst.String() + `","big":9007199254740993}}],` +
		`"processInstanceKey":"` + inst.String() + `"}`
	out, err := intKeysInJSON(keys, "/v2/user-tasks/search", []byte(body))
	if err != nil {
		t.Fatal(err)
	}

	var got struct {
		ProcessInstanceKey string `json:"processInstanceKey"`
		Items              []struct {
			ProcessInstanceKey   string                     `json:"processInstanceKey"`
			ProcessDefinitionKey string                     `json:"processDefinitionKey"`
			UserTaskKey          string                     `json:"userTaskKey"`
			ElementID            string                     `json:"elementId"`
			Variables            map[string]json.RawMessage `json:"variables"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	item := got.Items[0]
	if got.ProcessInstanceKey != "125" || item.ProcessInstanceKey != "125" || item.ProcessDefinitionKey != "126" || item.UserTaskKey != "127" {
		t.Fatalf("keys not rewritten: %s", out)
	}
	if item.ElementID != "review" {
		t.Fatalf("other fields changed: %s", out)
	}
	// Variables are data: left as they are, big integers included.
	if string(item.Variables["processInstanceKey"]) != `"`+inst.String()+`"` || string(item.Variables["big"]) != "9007199254740993" {
		t.Fatalf("variables changed: %s", out)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestIntKeysInJSON_NoKeys(t *testing.T) {
	keys, mock := mockKeys(t)
	body := []byte(`{"title":"NOT_FOUND","detail":"process instance not found"}`)
	out, err := intKeysInJSON(keys, "/v2/incidents/search", body)
	if err != nil || string(out) != string(body) {
		t.Fatalf("got %s, %v", out, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err) // no query at all
	}
}

func TestUUIDPath(t *testing.T) {
	keys, mock := mockKeys(t)
	id := uuid.New()

	cases := []struct {
		in, want string
		changed  bool
	}{
		{"/v2/jobs/42/completion", "/v2/jobs/42/completion", false},
		{"/v2/user-tasks/7/completion", "/v2/user-tasks/7/completion", false},
		{"/v2/process-instances/search", "/v2/process-instances/search", false},
		{"/v2/process-instances/" + id.String(), "/v2/process-instances/" + id.String(), false},
		{"/v2/decisions/123/evaluation", "/v2/decisions/123/evaluation", false}, // a decision id, not a key
	}
	for _, c := range cases {
		got, changed := uuidPath(keys, c.in)
		if got != c.want || changed != c.changed {
			t.Errorf("uuidPath(%s) = %s, %v; want %s, %v", c.in, got, changed, c.want, c.changed)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestIntKeysInJSON_ListsAndRouteFields(t *testing.T) {
	keys, mock := mockKeys(t)
	a, b, msg, dep, def := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	// A message publication: its key and the lists of instances.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT engine_key FROM public.process_instances WHERE id = $1")).WithArgs(a).
		WillReturnRows(sqlmock.NewRows([]string{"engine_key"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT engine_key FROM public.process_instances WHERE id = $1")).WithArgs(b).
		WillReturnRows(sqlmock.NewRows([]string{"engine_key"}).AddRow(2))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT engine_key FROM public.published_messages WHERE id = $1")).WithArgs(msg).
		WillReturnRows(sqlmock.NewRows([]string{"engine_key"}).AddRow(3))
	body := `{"messageKey":"` + msg.String() + `","correlatedProcessInstanceKeys":["` + a.String() + `","` + b.String() + `"],"startedProcessInstanceKeys":[]}`
	out, err := intKeysInJSON(keys, "/v2/messages/publication", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var pub struct {
		Key        string   `json:"messageKey"`
		Correlated []string `json:"correlatedProcessInstanceKeys"`
	}
	if err := json.Unmarshal(out, &pub); err != nil {
		t.Fatal(err)
	}
	if pub.Key != "3" || len(pub.Correlated) != 2 || pub.Correlated[0] != "1" || pub.Correlated[1] != "2" {
		t.Fatalf("publication: %s", out)
	}

	// A deployment: its id, and each definition's id under its process id.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT engine_key FROM public.deployments WHERE id = $1")).WithArgs(dep).
		WillReturnRows(sqlmock.NewRows([]string{"engine_key"}).AddRow(4))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT engine_key FROM public.process_definitions WHERE id = $1")).WithArgs(def).
		WillReturnRows(sqlmock.NewRows([]string{"engine_key"}).AddRow(5))
	body = `{"id":"` + dep.String() + `","deployedProcessDefinitions":{"invoice":{"id":"` + def.String() + `","key":"invoice","version":1}}}`
	out, err = intKeysInJSON(keys, "/v2/resources/deployments", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		ID   string `json:"id"`
		Defs map[string]struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		} `json:"deployedProcessDefinitions"`
	}
	if err := json.Unmarshal(out, &d); err != nil {
		t.Fatal(err)
	}
	if d.ID != "4" || d.Defs["invoice"].ID != "5" || d.Defs["invoice"].Key != "invoice" {
		t.Fatalf("deployment: %s", out)
	}

	// "key" means a key on the publication route only.
	other := []byte(`{"key":"` + msg.String() + `"}`)
	if out, err := intKeysInJSON(keys, "/v2/process-instances/search", other); err != nil || string(out) != string(other) {
		t.Fatalf("another route: %s, %v", out, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
