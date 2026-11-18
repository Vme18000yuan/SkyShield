package notificationwiring

import (
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestInboxDoesNotRequireLegacyDestinationTables(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	module, err := NewNotificationModule(db, "notification-test-owner")
	if err != nil || module == nil || module.InboxHandler == nil || module.OutboxWorker == nil || module.SSEHandler == nil {
		t.Fatalf("inbox-only module construction failed: %v", err)
	}
}
