package repository

import (
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

func TestProcessInstanceEngineKeyCachesResolution(t *testing.T) {
	ClearProcessInstanceKeyCache()
	t.Cleanup(ClearProcessInstanceKeyCache)

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := sqlx.NewDb(db, "postgres")
	id := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT engine_key FROM public.process_instances WHERE id = $1")).
		WithArgs(id).
		WillReturnRows(sqlmock.NewRows([]string{"engine_key"}).AddRow(int64(991)))

	for call := 1; call <= 100; call++ {
		key, err := ProcessInstanceEngineKey(q, id)
		if err != nil || key != 991 {
			t.Fatalf("call %d: key=%d err=%v", call, key, err)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("the UUID should have been resolved exactly once: %v", err)
	}
}

func TestRememberProcessInstanceKeyNeedsNoQuery(t *testing.T) {
	ClearProcessInstanceKeyCache()
	t.Cleanup(ClearProcessInstanceKeyCache)

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	id := uuid.New()
	RememberProcessInstanceKey(id, 1234)

	key, err := ProcessInstanceEngineKey(sqlx.NewDb(db, "postgres"), id)
	if err != nil || key != 1234 {
		t.Fatalf("key=%d err=%v", key, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
