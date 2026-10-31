package repository

import (
	"encoding/json"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

func newMockEngineRepo(t *testing.T) (*EngineRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	sqlxDB := sqlx.NewDb(db, "postgres")
	return NewEngineRepository(sqlxDB, nil), mock
}

// Regression: the graph cache must be keyed by process_definition_id, not
// process_instance_id, so two instances of the same definition share one
// cached graph: the second only looks up its definition id, it never reads
// parsed_graph again.
func TestGetProcessGraphByInstanceID_DedupesAcrossInstancesOfSameDefinition(t *testing.T) {
	ClearGraphCache()
	repo, mock := newMockEngineRepo(t)

	defID := uuid.New()
	instanceA := uuid.New()
	instanceB := uuid.New()
	graphJSON, _ := json.Marshal(map[string]interface{}{"nodes": map[string]interface{}{}})

	expectDefinitionLookup(mock, instanceA, defID)
	expectGraphRead(mock, defID, graphJSON)

	graphA, err := repo.GetProcessGraphByInstanceID(instanceA)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectDefinitionLookup(mock, instanceB, defID)

	graphB, err := repo.GetProcessGraphByInstanceID(instanceB)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if graphA != graphB {
		t.Errorf("expected instances of the same definition to share the cached graph pointer, got %p vs %p", graphA, graphB)
	}

	// A second call for instanceA must be a pure cache hit — no new
	// expectation registered, so an extra query here would fail the mock.
	graphA2, err := repo.GetProcessGraphByInstanceID(instanceA)
	if err != nil {
		t.Fatalf("unexpected error on cache hit: %v", err)
	}
	if graphA2 != graphA {
		t.Errorf("expected cache hit to return the same graph pointer")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestGetProcessGraphByInstanceID_EvictsOnOverflow(t *testing.T) {
	ClearGraphCache()
	repo, mock := newMockEngineRepo(t)
	graphJSON, _ := json.Marshal(map[string]interface{}{"nodes": map[string]interface{}{}})

	for i := 0; i < graphCacheMaxEntries+5; i++ {
		instanceID := uuid.New()
		defID := uuid.New()
		expectDefinitionLookup(mock, instanceID, defID)
		expectGraphRead(mock, defID, graphJSON)
		if _, err := repo.GetProcessGraphByInstanceID(instanceID); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	graphCacheMu.RLock()
	size := len(graphCache)
	graphCacheMu.RUnlock()
	if size > graphCacheMaxEntries {
		t.Errorf("expected graphCache to stay bounded at %d entries, got %d", graphCacheMaxEntries, size)
	}
}

func expectDefinitionLookup(mock sqlmock.Sqlmock, instanceID, defID uuid.UUID) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT process_definition_id FROM public.process_instances WHERE id = $1")).
		WithArgs(instanceID).
		WillReturnRows(sqlmock.NewRows([]string{"process_definition_id"}).AddRow(defID))
}

func expectGraphRead(mock sqlmock.Sqlmock, defID uuid.UUID, graphJSON []byte) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT parsed_graph FROM public.process_definitions WHERE id = $1")).
		WithArgs(defID).
		WillReturnRows(sqlmock.NewRows([]string{"parsed_graph"}).AddRow(graphJSON))
}
