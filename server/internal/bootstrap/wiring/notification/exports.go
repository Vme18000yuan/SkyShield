// Package notificationwiring assembles the notification module at the
// composition root without leaking its persistence or provider details.
package notificationwiring

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/yyhuni/lunafox/server/internal/modules/notification/application"
	"github.com/yyhuni/lunafox/server/internal/modules/notification/handler"
	"github.com/yyhuni/lunafox/server/internal/modules/notification/repository"
	"gorm.io/gorm"
)

// Module contains the notification boundaries and managed workers that must
// share one process-local realtime broker and one database composition root.
type Module struct {
	InboxHandler *handler.NotificationInboxHandler
	SSEHandler   *handler.NotificationSSEHandler
	OutboxWorker *application.OutboxWorker
	RetentionJob *application.RetentionJob
}

// NewNotificationWorkerOwner creates a process-unique lease owner. The owner
// is persisted only while a worker holds a lease and is never user input.
func NewNotificationWorkerOwner() (string, error) {
	entropy := make([]byte, 16)
	if _, err := rand.Read(entropy); err != nil {
		return "", fmt.Errorf("generate notification worker owner: %w", err)
	}
	return "notification-" + hex.EncodeToString(entropy), nil
}

// NewNotificationModule wires handlers, workers, repositories, templates,
// and the inbox retention job around one notification database and lease owner.
func NewNotificationModule(db *gorm.DB, workerOwner string) (*Module, error) {
	if db == nil {
		panic("notification wiring database is required")
	}
	workerOwner = strings.TrimSpace(workerOwner)
	if workerOwner == "" {
		panic("notification wiring worker owner is required")
	}

	outboxStore := repository.NewOutboxRepository(db)
	inboxStore := repository.NewInboxRepository(db)
	deliveryStore := repository.NewDeliveryRepository(db)
	realtimeHub := application.NewRealtimeHub()
	materializer := application.NewMaterializer(
		repository.NewTransactionCoordinator(db),
		repository.NewFactRepository(db),
		repository.NewAudienceRepository(db),
		nil, // SkyShield delivers external messages through account-owned AI channels.
		application.NewTemplates(),
		realtimeHub,
	)
	inboxService := application.NewInboxService(inboxStore, inboxStore)

	return &Module{
		InboxHandler: handler.NewNotificationInboxHandler(inboxService),
		SSEHandler:   handler.NewNotificationSSEHandler(realtimeHub),
		OutboxWorker: application.NewOutboxWorker(outboxStore, materializer, application.OutboxWorkerOptions{
			Owner: workerOwner + "-outbox",
		}),
		RetentionJob: application.NewRetentionJob(outboxStore, inboxStore, deliveryStore, application.RetentionJobOptions{}),
	}, nil
}
