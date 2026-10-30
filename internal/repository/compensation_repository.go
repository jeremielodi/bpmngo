package repository

import (
	"strconv"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// Compensation bookkeeping. Each time an activity that has a compensation
// handler completes, a 'compensable' event subscription records it
// (event_name = the activity, target_element_id = its handler), in the same
// transaction as the completion. A compensation throw event consumes the
// records of its scope's activities and runs one handler per record.
//
// These records describe work already done, so cancelling or closing a
// scope must not delete them: compensation typically runs right after an
// error interrupted the scope (deleteWaitStateTx and TerminateOtherWorkTx
// skip them). Archiving the instance removes them with everything else.

const compensationHandlerPrefix = "compensation:"

// RecordCompensableTx records that activityID, run by execID, completed
// and can be compensated by handlerID.
func (r *EngineRepository) RecordCompensableTx(tx *sqlx.Tx, instanceID uuid.UUID, execID int64, activityID, handlerID string) error {
	instanceKey, err := processInstanceEngineKey(tx, instanceID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		INSERT INTO event_subscriptions
		  (process_instance_key, execution_key, event_type, event_name, target_element_id)
		VALUES ($1, $2, 'compensable', $3, $4)
	`, instanceKey, execID, activityID, handlerID)
	return err
}

// Compensable is a completed activity waiting to be compensated.
type Compensable struct {
	ActivityID string `db:"event_name"`
	HandlerID  string `db:"target_element_id"`
}

// TakeCompensablesTx consumes the not yet compensated completions of the
// given activities, so each one is compensated once.
func (r *EngineRepository) TakeCompensablesTx(tx *sqlx.Tx, instanceID uuid.UUID, activityIDs []string) ([]Compensable, error) {
	if len(activityIDs) == 0 {
		return nil, nil
	}
	instanceKey, err := processInstanceEngineKey(tx, instanceID)
	if err != nil {
		return nil, err
	}
	var out []Compensable
	err = tx.Select(&out, `
		UPDATE event_subscriptions
		SET consumed_at = NOW()
		WHERE process_instance_key = $1
		  AND event_type = 'compensable' AND consumed_at IS NULL
		  AND event_name = ANY($2::text[])
		RETURNING event_name, target_element_id
	`, instanceKey, pq.Array(activityIDs))
	return out, err
}

// CompensationHandlerPathID is the path_id of an execution running a
// compensation handler for the compensation throw event executing in
// throwExecID. Handler executions have no parent — as a child they'd make
// the thrower look like a fork parent to the live-work and join logic — so
// the link to the thrower lives in path_id.
func CompensationHandlerPathID(throwExecID int64) string {
	return compensationHandlerPrefix + strconv.FormatInt(throwExecID, 10)
}

// ParseCompensationHandlerPathID returns the thrower a handler execution
// reports to; ok is false for any other execution.
func ParseCompensationHandlerPathID(pathID *string) (throwExecID int64, ok bool) {
	if pathID == nil || len(*pathID) <= len(compensationHandlerPrefix) || (*pathID)[:len(compensationHandlerPrefix)] != compensationHandlerPrefix {
		return 0, false
	}
	id, err := strconv.ParseInt((*pathID)[len(compensationHandlerPrefix):], 10, 64)
	return id, err == nil
}

// CountRunningCompensationHandlersTx counts the handler executions of a
// thrower that are still running.
func (r *EngineRepository) CountRunningCompensationHandlersTx(tx *sqlx.Tx, instanceID uuid.UUID, throwExecID int64) (int, error) {
	var n int
	err := tx.Get(&n, `
		SELECT count(*) FROM public.executions
		WHERE process_instance_key = (SELECT engine_key FROM public.process_instances WHERE id = $1)
		  AND path_id = $2 AND status IN ('active', 'waiting')
	`, instanceID, CompensationHandlerPathID(throwExecID))
	return n, err
}
