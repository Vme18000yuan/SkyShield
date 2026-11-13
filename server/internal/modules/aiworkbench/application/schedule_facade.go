package application

import (
	"context"
	"sort"
	"strings"
	"unicode/utf8"

	artexdb "github.com/Autumn-27/artex/db"
)

type ScheduleFacade struct {
	store      ScheduleStore
	controller *ScheduleController
}

func NewScheduleFacade(store ScheduleStore, controller *ScheduleController) *ScheduleFacade {
	return &ScheduleFacade{store: store, controller: controller}
}
func (f *ScheduleFacade) List(ctx context.Context, user int) ([]ScheduleRecord, error) {
	if user <= 0 {
		return nil, ErrInvalidInput
	}
	return f.store.ListSchedules(ctx, user)
}
func (f *ScheduleFacade) Get(ctx context.Context, user int, id int64) (*ScheduleRecord, []ScheduleHistory, error) {
	if user <= 0 || id <= 0 {
		return nil, nil, ErrInvalidInput
	}
	item, err := f.store.GetSchedule(ctx, user, id)
	if err != nil {
		return nil, nil, err
	}
	history, err := f.store.ScheduleHistory(ctx, user, id)
	return item, history, err
}
func (f *ScheduleFacade) Save(ctx context.Context, id int64, input ScheduleInput) (*ScheduleRecord, error) {
	c := &input.Calendar
	c.Name = strings.TrimSpace(c.Name)
	if c.TimezoneMode == "" {
		c.TimezoneMode = "beijing"
	}
	if input.UserID <= 0 || id < 0 || utf8.RuneCountInString(c.Name) > 200 || len(input.Remark) > 16*1024 || artexdb.ValidateTaskScheduleInput(*c) != nil {
		return nil, ErrInvalidInput
	}
	if c.ScheduleType == "once" {
		c.Weekdays = nil
	} else {
		c.RunDate = ""
		c.EndDate = ""
		sort.Ints(c.Weekdays)
	}
	item, err := f.store.SaveSchedule(ctx, id, input)
	if err == nil && f.controller != nil {
		f.controller.NotifyTasks()
	}
	return item, err
}

// UpdateMetadata changes only display fields, so a rename cannot reopen a missed window.
func (f *ScheduleFacade) UpdateMetadata(ctx context.Context, user int, id int64, name, remark *string) (*ScheduleRecord, error) {
	if user <= 0 || id <= 0 || name == nil && remark == nil {
		return nil, ErrInvalidInput
	}
	if name != nil {
		value := strings.TrimSpace(*name)
		if value == "" || utf8.RuneCountInString(value) > 200 {
			return nil, ErrInvalidInput
		}
		name = &value
	}
	if remark != nil && utf8.RuneCountInString(*remark) > 2000 {
		return nil, ErrInvalidInput
	}
	store, ok := f.store.(ScheduleMetadataStore)
	if !ok {
		return nil, ErrInvalidInput
	}
	return store.UpdateScheduleMetadata(ctx, user, id, name, remark)
}
func (f *ScheduleFacade) Delete(ctx context.Context, user int, id int64) error {
	if user <= 0 || id <= 0 {
		return ErrInvalidInput
	}
	err := f.store.DeleteSchedule(ctx, user, id)
	if err == nil && f.controller != nil {
		f.controller.Notify()
	}
	return err
}
func (f *ScheduleFacade) SetEnabled(ctx context.Context, user int, id int64, enabled bool) error {
	if user <= 0 || id <= 0 {
		return ErrInvalidInput
	}
	err := f.store.SetScheduleEnabled(ctx, user, id, enabled)
	if err == nil && f.controller != nil {
		f.controller.NotifyTasks()
	}
	return err
}
func (f *ScheduleFacade) RunNow(ctx context.Context, user int, id int64) (ScheduleRunResult, error) {
	if user <= 0 || id <= 0 {
		return ScheduleRunResult{}, ErrInvalidInput
	}
	n, err := f.store.RunScheduleNow(ctx, user, id)
	if err == nil && f.controller != nil {
		f.controller.NotifyTasks()
	}
	return n, err
}
