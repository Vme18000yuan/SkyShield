package application

import (
	"context"
	"errors"
	"time"

	artexdb "github.com/Autumn-27/artex/db"
)

var ErrScheduleNotFound = errors.New("AI schedule not found")

// ScheduleRecord retains ARTEX calendar semantics while binding them to a
// single Tianqiong tenant and owner.
type ScheduleRecord struct {
	Item        artexdb.TaskSchedule
	OwnerUserID int
	TenantID    string
	RunCount    int
	LastRunAt   *time.Time
}

type ScheduleInput struct {
	UserID          int
	Remark          string
	Calendar        artexdb.TaskScheduleInput
	PreserveRemark  bool
	PreserveEnabled bool
}

type ScheduleMetadataStore interface {
	UpdateScheduleMetadata(context.Context, int, int64, *string, *string) (*ScheduleRecord, error)
}

type ScheduleHistory struct {
	ID        int64
	TaskID    *int64
	Action    string
	Success   bool
	Message   string
	CreatedAt time.Time
}

type ScheduleTickResult struct {
	PausedTaskIDs []int64
	Changed       bool
}

type ScheduleRunResult struct {
	Started int
	Errors  []string
}

type ScheduleStore interface {
	ListSchedules(context.Context, int) ([]ScheduleRecord, error)
	GetSchedule(context.Context, int, int64) (*ScheduleRecord, error)
	SaveSchedule(context.Context, int64, ScheduleInput) (*ScheduleRecord, error)
	DeleteSchedule(context.Context, int, int64) error
	SetScheduleEnabled(context.Context, int, int64, bool) error
	RunScheduleNow(context.Context, int, int64) (ScheduleRunResult, error)
	ScheduleHistory(context.Context, int, int64) ([]ScheduleHistory, error)
	TickSchedules(context.Context, time.Time, time.Time) (ScheduleTickResult, error)
}
