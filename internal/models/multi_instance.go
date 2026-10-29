// internal/models/multi_instance.go
package models

import (
	"time"

	"github.com/google/uuid"
)

// MultiInstanceExecution tracks a multi-instance activity
type MultiInstanceExecution struct {
	ID                     uuid.UUID `db:"id"`
	ProcessInstanceID      uuid.UUID `db:"process_instance_id"`
	ExecutionID            int64     `db:"execution_key"`
	ActivityID             string    `db:"activity_id"`
	IsSequential           bool      `db:"is_sequential"`
	TotalCount             int       `db:"total_count"`
	CompletedCount         int       `db:"completed_count"`
	NrOfActiveInstances    int       `db:"nr_of_active_instances"`
	NrOfCompletedInstances int       `db:"nr_of_completed_instances"`
	LoopCounter            int       `db:"loop_counter"`
	CompletionCondition    *string   `db:"completion_condition"`
	ElementVariable        *string   `db:"element_variable"`
	InputCollection        *string   `db:"input_collection"`  // Variable name containing input list
	OutputCollection       *string   `db:"output_collection"` // Variable name for output list
	Status                 string    `db:"status"`            // active, completed
	CreatedAt              time.Time `db:"created_at"`
	UpdatedAt              time.Time `db:"updated_at"`
}

// MultiInstanceChild tracks each child instance
type MultiInstanceChild struct {
	ID                uuid.UUID  `db:"id"`
	ParentExecutionID int64      `db:"parent_execution_key"`
	ChildExecutionID  int64      `db:"child_execution_key"`
	LoopIndex         int        `db:"loop_index"`
	ElementValue      string     `db:"element_value"` // Value for this iteration
	Status            string     `db:"status"`        // active, completed
	CreatedAt         time.Time  `db:"created_at"`
	CompletedAt       *time.Time `db:"completed_at"`
}
