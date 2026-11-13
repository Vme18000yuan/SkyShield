package aiapprovalwiring

import (
	app "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/application"
	"github.com/yyhuni/lunafox/server/internal/modules/aiapproval/repository"
	"gorm.io/gorm"
)

func NewStore(db *gorm.DB) app.Store { return repository.NewStore(db) }
