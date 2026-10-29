package repository

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/ids"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// Embedded sub-process support. Entering a sub-process parks the entering
// execution (the "scope execution") on the sub-process node and creates a
// child execution at the inner start event; anything inside — forks,
// multi-instance children — hangs below that child in the
// parent_execution_id tree.
//
// These helpers walk that tree in Go from one per-instance query instead of
// a WITH RECURSIVE query, which Citus can't plan against a distributed
// executions table.

type executionLink struct {
	ID                int64  `db:"engine_key"`
	ParentExecutionID *int64 `db:"parent_execution_key"`
	CurrentElementID  string `db:"current_element_id"`
	Status            string `db:"status"`
}

func loadExecutionTreeTx(tx *sqlx.Tx, instanceID uuid.UUID) (map[int64]*executionLink, error) {
	instanceKey, err := processInstanceEngineKey(tx, instanceID)
	if err != nil {
		return nil, err
	}
	var rows []executionLink
	if err := tx.Select(&rows, `
		SELECT engine_key, parent_execution_key, current_element_id, status
		FROM public.executions
		WHERE process_instance_key = $1
	`, instanceKey); err != nil {
		return nil, err
	}
	byID := make(map[int64]*executionLink, len(rows))
	for i := range rows {
		byID[rows[i].ID] = &rows[i]
	}
	return byID, nil
}

// descendants returns the IDs of every execution below root (root excluded).
func descendants(tree map[int64]*executionLink, root int64) []int64 {
	children := map[int64][]int64{}
	for _, e := range tree {
		if e.ParentExecutionID != nil {
			children[*e.ParentExecutionID] = append(children[*e.ParentExecutionID], e.ID)
		}
	}
	var out []int64
	stack := append([]int64(nil), children[root]...)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		out = append(out, id)
		stack = append(stack, children[id]...)
	}
	return out
}

// FindScopeExecutionTx returns the nearest ancestor of execID that is parked
// on the sub-process node scopeNodeID, and locks its row so concurrent
// branches finishing inside the same sub-process are serialized.
func (r *EngineRepository) FindScopeExecutionTx(tx *sqlx.Tx, instanceID uuid.UUID, execID int64, scopeNodeID string) (int64, error) {
	tree, err := loadExecutionTreeTx(tx, instanceID)
	if err != nil {
		return 0, err
	}
	self, ok := tree[execID]
	if !ok {
		return 0, fmt.Errorf("execution %d not found", execID)
	}
	instanceKey, err := processInstanceEngineKey(tx, instanceID)
	if err != nil {
		return 0, err
	}
	for parent := self.ParentExecutionID; parent != nil; {
		p, ok := tree[*parent]
		if !ok {
			break
		}
		if p.CurrentElementID == scopeNodeID && p.Status == "waiting" {
			var locked int64
			if err := tx.Get(&locked, `
				SELECT engine_key FROM public.executions
				WHERE process_instance_key = $1
				  AND engine_key = $2
				FOR UPDATE
			`, instanceKey, p.ID); err != nil {
				return 0, err
			}
			return locked, nil
		}
		parent = p.ParentExecutionID
	}
	return 0, fmt.Errorf("no execution parked on sub-process %s above execution %d", scopeNodeID, execID)
}

// HasLiveWorkUnderTx reports whether anything inside the scope execution is
// still running. A descendant counts when it is active/waiting and has no
// children of its own: a fork parent whose branches all finished is left
// "waiting" with only completed children, and must not keep the
// sub-process open. Executions parked on a node in ignoreNodes don't count.
func (r *EngineRepository) HasLiveWorkUnderTx(tx *sqlx.Tx, instanceID uuid.UUID, scopeExecID int64, ignoreNodes map[string]bool) (bool, error) {
	tree, err := loadExecutionTreeTx(tx, instanceID)
	if err != nil {
		return false, err
	}
	return anyLiveLeaf(tree, descendants(tree, scopeExecID), ignoreNodes), nil
}

// CloseScopeTx marks every still-open execution below the scope execution
// as completed — the leftover fork parents and armed event sub-process
// triggers a finished sub-process leaves behind — and removes those
// triggers' timers and subscriptions.
func (r *EngineRepository) CloseScopeTx(tx *sqlx.Tx, instanceID uuid.UUID, scopeExecID int64) error {
	tree, err := loadExecutionTreeTx(tx, instanceID)
	if err != nil {
		return err
	}
	ids := descendants(tree, scopeExecID)
	if len(ids) == 0 {
		return nil
	}
	arr := pq.Array(ids)
	instanceKey, err := processInstanceEngineKey(tx, instanceID)
	if err != nil {
		return err
	}
	if err := r.DisarmBoundaryTriggersTx(tx, instanceID, ids...); err != nil {
		return err
	}
	if _, err = tx.Exec(`
		UPDATE public.executions
		SET status = 'completed', is_active = false, updated_at = NOW()
		WHERE process_instance_key = $1
		  AND engine_key = ANY($2::bigint[]) AND status IN ('active', 'waiting')
	`, instanceKey, arr); err != nil {
		return err
	}
	return deleteWaitStateTx(tx, instanceID, arr)
}

// deleteWaitStateTx removes the untriggered timers and unconsumed message/
// signal subscriptions of the given executions.
func deleteWaitStateTx(tx *sqlx.Tx, instanceID uuid.UUID, execIDs interface{}) error {
	instanceKey, err := processInstanceEngineKey(tx, instanceID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`
		DELETE FROM public.timer_jobs
		WHERE process_instance_key = $1
		  AND execution_key = ANY($2::bigint[]) AND is_triggered = false
	`, instanceKey, execIDs); err != nil {
		return err
	}
	_, err = tx.Exec(`
		DELETE FROM public.event_subscriptions
		WHERE process_instance_key = $1
		  AND execution_key = ANY($2::bigint[]) AND consumed_at IS NULL AND event_type <> 'compensable'
	`, instanceKey, execIDs)
	return err
}

// CancelExecutionWorkTx clears what holds one execution at its element, so
// the token can be moved elsewhere: its open user tasks and pending/locked
// jobs are cancelled, its untriggered timers and subscriptions deleted, its
// armed boundary events disarmed and its open incidents resolved. The
// execution itself is left as is, for the caller to move. Returns the
// cancelled task and job IDs for audit logging.
func (r *EngineRepository) CancelExecutionWorkTx(tx *sqlx.Tx, instanceID uuid.UUID, execID int64) (cancelledTasks, cancelledJobs []int64, err error) {
	instanceKey, err := processInstanceEngineKey(tx, instanceID)
	if err != nil {
		return nil, nil, err
	}
	if cancelledTasks, cancelledJobs, err = r.CancelActivityWorkTx(tx, instanceID, execID); err != nil {
		return nil, nil, err
	}
	if err = r.DisarmBoundaryTriggersTx(tx, instanceID, execID); err != nil {
		return nil, nil, err
	}
	if err = deleteWaitStateTx(tx, instanceID, pq.Array([]int64{execID})); err != nil {
		return nil, nil, err
	}
	_, err = tx.Exec(`
		UPDATE public.incidents SET state = 'resolved', resolved_at = NOW()
		WHERE process_instance_key = $1 AND state = 'open'
		  AND (execution_key = $2 OR job_key IN (
		      SELECT engine_key FROM public.jobs
		      WHERE process_instance_key = $1
		        AND execution_key = $2))
	`, instanceKey, execID)
	if err != nil {
		return nil, nil, err
	}
	return cancelledTasks, cancelledJobs, nil
}

// CancelScopeTx interrupts everything below rootExecID except keepExecID
// (uuid.Nil keeps nothing): terminates the executions, cancels their open
// user tasks and pending/locked jobs, and deletes their untriggered timers
// and subscriptions. Used when an interrupting boundary event or event
// sub-process fires on a sub-process. Returns the cancelled task and job
// IDs for audit logging; a no-op when there is nothing to interrupt.
func (r *EngineRepository) CancelScopeTx(tx *sqlx.Tx, instanceID uuid.UUID, rootExecID, keepExecID int64) (cancelledTasks, cancelledJobs []int64, err error) {
	tree, err := loadExecutionTreeTx(tx, instanceID)
	if err != nil {
		return nil, nil, err
	}
	ids := slices.DeleteFunc(descendants(tree, rootExecID), func(id int64) bool { return id == keepExecID })
	if len(ids) == 0 {
		return nil, nil, nil
	}
	arr := pq.Array(ids)
	instanceKey, err := processInstanceEngineKey(tx, instanceID)
	if err != nil {
		return nil, nil, err
	}
	if err = r.DisarmBoundaryTriggersTx(tx, instanceID, ids...); err != nil {
		return nil, nil, err
	}

	if _, err = tx.Exec(`
		UPDATE public.executions
		SET status = 'terminated', is_active = false, updated_at = NOW()
		WHERE process_instance_key = $1
		  AND engine_key = ANY($2::bigint[]) AND status IN ('active', 'waiting')
	`, instanceKey, arr); err != nil {
		return nil, nil, err
	}
	if err = tx.Select(&cancelledTasks, `
		UPDATE public.tasks
		SET status = 'cancelled', updated_at = NOW()
		WHERE process_instance_key = $1
		  AND execution_key = ANY($2::bigint[]) AND status IN ('created', 'claimed')
		RETURNING engine_key
	`, instanceKey, arr); err != nil {
		return nil, nil, err
	}
	if err = tx.Select(&cancelledJobs, `
		UPDATE public.jobs
		SET status = 'cancelled', updated_at = NOW()
		WHERE process_instance_key = $1
		  AND execution_key = ANY($2::bigint[]) AND status IN ('pending', 'locked')
		RETURNING engine_key
	`, instanceKey, arr); err != nil {
		return nil, nil, err
	}
	if err = deleteWaitStateTx(tx, instanceID, arr); err != nil {
		return nil, nil, err
	}
	return cancelledTasks, cancelledJobs, nil
}

// TerminateOtherWorkTx stops every token of the instance except keepExecID,
// for a terminate end event outside any sub-process: terminates the other
// executions, cancels open user tasks and pending/locked jobs, and deletes
// untriggered timers. Returns cancelled task and job IDs for audit logging.
func (r *EngineRepository) TerminateOtherWorkTx(tx *sqlx.Tx, instanceID uuid.UUID, keepExecID int64) (cancelledTasks, cancelledJobs []int64, err error) {
	instanceKey, err := processInstanceEngineKey(tx, instanceID)
	if err != nil {
		return nil, nil, err
	}
	if _, err = tx.Exec(`
		UPDATE public.executions
		SET status = 'terminated', is_active = false, updated_at = NOW()
		WHERE process_instance_key = $1
		  AND engine_key <> $2 AND status IN ('active', 'waiting')
	`, instanceKey, keepExecID); err != nil {
		return nil, nil, err
	}
	if err = tx.Select(&cancelledTasks, `
		UPDATE public.tasks
		SET status = 'cancelled', updated_at = NOW()
		WHERE process_instance_key = $1
		  AND status IN ('created', 'claimed')
		RETURNING engine_key
	`, instanceKey); err != nil {
		return nil, nil, err
	}
	if err = tx.Select(&cancelledJobs, `
		UPDATE public.jobs
		SET status = 'cancelled', updated_at = NOW()
		WHERE process_instance_key = $1
		  AND status IN ('pending', 'locked')
		RETURNING engine_key
	`, instanceKey); err != nil {
		return nil, nil, err
	}
	if _, err = tx.Exec(`
		DELETE FROM public.timer_jobs
		WHERE process_instance_key = $1
		  AND is_triggered = false
	`, instanceKey); err != nil {
		return nil, nil, err
	}
	if _, err = tx.Exec(`
		DELETE FROM public.event_subscriptions
		WHERE process_instance_key = $1
		  AND consumed_at IS NULL AND event_type <> 'compensable'
		  AND (execution_key IS NULL OR execution_key <> $2)
	`, instanceKey, keepExecID); err != nil {
		return nil, nil, err
	}
	return cancelledTasks, cancelledJobs, nil
}

// LockProcessInstanceTx locks the process instance row, serializing the
// tokens of one instance that reach an end event at the same moment.
func (r *EngineRepository) LockProcessInstanceTx(tx *sqlx.Tx, instanceID uuid.UUID) error {
	instanceKey, err := processInstanceEngineKey(tx, instanceID)
	if err != nil {
		return err
	}
	var lockedKey int64
	return tx.Get(&lockedKey, `SELECT engine_key FROM public.process_instances WHERE engine_key = $1 FOR UPDATE`, instanceKey)
}

// HasLiveWorkInInstanceTx reports whether any token of the instance is
// still running — an active/waiting execution with no children of its own
// (see HasLiveWorkUnderTx). Executions parked on a node in ignoreNodes
// don't count.
func (r *EngineRepository) HasLiveWorkInInstanceTx(tx *sqlx.Tx, instanceID uuid.UUID, ignoreNodes map[string]bool) (bool, error) {
	tree, err := loadExecutionTreeTx(tx, instanceID)
	if err != nil {
		return false, err
	}
	all := make([]int64, 0, len(tree))
	for id := range tree {
		all = append(all, id)
	}
	return anyLiveLeaf(tree, all, ignoreNodes), nil
}

// anyLiveLeaf reports whether one of ids is an active/waiting execution
// with no children. Armed triggers — executions waiting on a node in
// ignoreNodes — are neither live work nor children that make their parent
// a non-leaf.
func anyLiveLeaf(tree map[int64]*executionLink, ids []int64, ignoreNodes map[string]bool) bool {
	armed := func(e *executionLink) bool {
		return e.Status == "waiting" && ignoreNodes[e.CurrentElementID]
	}
	hasChildren := map[int64]bool{}
	for _, e := range tree {
		if e.ParentExecutionID != nil && !armed(e) {
			hasChildren[*e.ParentExecutionID] = true
		}
	}
	for _, id := range ids {
		e := tree[id]
		if (e.Status == "active" || e.Status == "waiting") && !hasChildren[id] && !armed(e) {
			return true
		}
	}
	return false
}

// CreateErrorTriggerTx creates the trigger execution for an error event
// sub-process (eventSubNodeID) that caught a BPMN error, under the
// execution of the scope it sits in — resolved from throwingExecID, nil at
// process level — and returns its ID. Running it (runtime's
// executeEventSubProcess) interrupts the scope and starts the body.
func (r *EngineRepository) CreateErrorTriggerTx(tx *sqlx.Tx, instanceID uuid.UUID, throwingExecID int64, eventSubNodeID, scope string) (int64, error) {
	var parent *int64
	if scope != "" {
		scopeExecID, err := r.FindScopeExecutionTx(tx, instanceID, throwingExecID, scope)
		if err != nil {
			return 0, err
		}
		parent = &scopeExecID
	}
	id := ids.NewEngineKey()
	pathID := strconv.FormatInt(id, 10) + "_" + eventSubNodeID
	if err := r.CreateExecutionTx(tx, &Execution{
		ID:                id,
		ProcessInstanceID: instanceID,
		ParentExecutionID: parent,
		CurrentElementID:  eventSubNodeID,
		Status:            "active",
		IsActive:          true,
		PathID:            &pathID,
	}); err != nil {
		return 0, fmt.Errorf("failed to create error event sub-process trigger: %w", err)
	}
	return id, nil
}
