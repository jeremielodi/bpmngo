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
// ActivateJobs back to CompleteJob/FailJob later.
func TestGetOrAssignKey_NewKey(t *testing.T) {
	repo, mock := newMockZeebeKeyRepo(t)
	resourceID := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT engine_key FROM public.process_instances WHERE id = $1")).
		WithArgs(resourceID).
		WillReturnRows(sqlmock.NewRows([]string{"engine_key"}).AddRow(int64(42)))

	for i := 0; i < 2; i++ { // the second call is served from memory: no query
		key, err := repo.GetOrAssignKey("process_instance", resourceID)
		if err != nil || key != 42 {
			t.Fatalf("call %d: key %d, err %v; want 42", i+1, key, err)
		}
	}
	// The reverse lookup too.
	if id, err := repo.ResolveKey("process_instance", 42); err != nil || id != resourceID {
		t.Fatalf("ResolveKey: %s, %v", id, err)
	}
}

// Issued earlier (by another BpmnGo instance, or before a restart): the
// insert does nothing and returns no row, the stored key is read instead —
// never a new key.
func TestGetOrAssignKey_IssuedEarlier(t *testing.T) {
	repo, mock := newMockZeebeKeyRepo(t)
	resourceID := uuid.New()

	// Signals have no persisted engine entity and intentionally keep using
	// the compatibility registry.
	mock.ExpectQuery(`INSERT INTO public\.zeebe_keys .*DO NOTHING`).
		WithArgs("signal", resourceID).
		WillReturnRows(sqlmock.NewRows([]string{"key"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT key FROM public.zeebe_keys WHERE resource_type = $1 AND resource_id = $2")).
		WithArgs("signal", resourceID).
		WillReturnRows(sqlmock.NewRows([]string{"key"}).AddRow(int64(7)))

	key, err := repo.GetOrAssignKey("signal", resourceID)
	if err != nil || key != 7 {
		t.Fatalf("key %d, err %v; want the stored 7", key, err)
	}
}

func TestResolveKey(t *testing.T) {
	repo, mock := newMockZeebeKeyRepo(t)
	resourceID := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM public.process_instances WHERE engine_key = $1")).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(resourceID))

	got, err := repo.ResolveKey("process_instance", 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != resourceID {
		t.Errorf("expected %s, got %s", resourceID, got)
	}
}
