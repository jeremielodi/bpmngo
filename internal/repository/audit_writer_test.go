package repository

import (
	"context"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/models"
	"github.com/jmoiron/sqlx"
)

// Queued rows go out in one INSERT, and FlushAuditLogs writes them before
// returning: a reader that flushes first sees every row logged before it.
func TestAuditWriter_BatchesAndFlushes(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sqlxDB := sqlx.NewDb(db, "postgres")

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		auditWriterMu.Lock()
		auditBatches = nil
		auditWriterMu.Unlock()
	}()
	StartAuditWriter(ctx, sqlxDB)
	instance := uuid.New()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, engine_key FROM public.process_instances WHERE id = ANY($1::uuid[])")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "engine_key"}).AddRow(instance, int64(42)))
	mock.ExpectExec(regexp.QuoteMeta("SET LOCAL synchronous_commit TO OFF")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO public.audit_logs") + `.*\(\$1, \$2, \$3, \$4, \$5, \$6, \$7, \$8\), \(\$9, .*\(\$17, `).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	repo := NewAuditRepository(sqlxDB)
	for _, action := range []models.AuditAction{models.ActionProcessStarted, models.ActionExecutionMoved, models.ActionProcessCompleted} {
		if err := repo.CreateAuditLog(&models.AuditLog{ProcessInstanceID: &instance, Action: action}); err != nil {
			t.Fatal(err)
		}
	}
	FlushAuditLogs()

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("the three rows should go out in one statement, before FlushAuditLogs returns: %v", err)
	}
}
