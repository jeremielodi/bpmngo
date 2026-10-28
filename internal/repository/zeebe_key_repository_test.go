package repository

import (
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

func newMockZeebeKeyRepo(t *testing.T) (*ZeebeKeyRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
		db.Close()
	})
	sqlxDB := sqlx.NewDb(db, "postgres")
	return NewZeebeKeyRepository(sqlxDB), mock
}

// A second call for the same resource must return the SAME key, not fail or
// assign a new one — this is what lets a client hand a job key from
// ActivateJobs back to CompleteJob/FailJob later. That guarantee lives in
// the SQL (an upsert that RETURNs the stored key on conflict; DO NOTHING
// would return no row), so the statement shape is what is checked here.
// The conflict itself needs a real database: see the gRPC e2e suites.
func TestGetOrAssignKey(t *testing.T) {
	repo, mock := newMockZeebeKeyRepo(t)
	resourceID := uuid.New()

	mock.ExpectQuery(`INSERT INTO public\.zeebe_keys .*ON CONFLICT \(resource_type, resource_id\)\s+DO UPDATE .*RETURNING key`).
		WithArgs("job", resourceID).
		WillReturnRows(sqlmock.NewRows([]string{"key"}).AddRow(int64(42)))

	key, err := repo.GetOrAssignKey("job", resourceID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != 42 {
		t.Errorf("expected key 42, got %d", key)
	}
}

func TestResolveKey(t *testing.T) {
	repo, mock := newMockZeebeKeyRepo(t)
	resourceID := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT resource_id FROM public.zeebe_keys")).
		WithArgs("process_instance", int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"resource_id"}).AddRow(resourceID))

	got, err := repo.ResolveKey("process_instance", 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != resourceID {
		t.Errorf("expected %s, got %s", resourceID, got)
	}
}
