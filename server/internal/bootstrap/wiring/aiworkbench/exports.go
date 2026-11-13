package aiworkbenchwiring

import (
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/gorm"
)

func NewAIExecutionPermitStore(db *gorm.DB) airuntime.ExecutionPermitStore {
	return airepo.NewExecutionPermitStore(db)
}

func NewAIWorkbenchAgentTriggerStoreAdapter(db *gorm.DB) aiapp.AgentTriggerJobStore {
	return airepo.NewAgentTriggerStore(db)
}

func NewAIWorkbenchConversationStoreAdapter(db *gorm.DB) aiapp.ConversationStore {
	return airepo.NewConversationStore(db)
}

func NewAIWorkbenchChatAttachmentStoreAdapter(db *gorm.DB) aiapp.ChatAttachmentStore {
	return airepo.NewConversationStore(db)
}

func NewAIWorkbenchSideQuestionStoreAdapter(db *gorm.DB) aiapp.SideQuestionStore {
	return airepo.NewConversationStore(db)
}

func NewAIWorkbenchTaskStoreAdapter(db *gorm.DB) aiapp.TaskStore { return airepo.NewTaskStore(db) }

func NewAIWorkbenchIntelligenceStoreAdapter(db *gorm.DB) aiapp.IntelligenceStore {
	return airepo.NewTaskIntelligenceStore(db)
}

func NewAIWorkbenchCredentialJobStoreAdapter(db *gorm.DB) aiapp.CredentialJobStore {
	return airepo.NewCredentialJobStore(db)
}

func NewAIWorkbenchScheduleStoreAdapter(db *gorm.DB) aiapp.ScheduleStore {
	return airepo.NewScheduleStore(db)
}

func NewAIWorkbenchChatMentionStoreAdapter(db *gorm.DB) aiapp.ChatMentionStore {
	return airepo.NewChatMentionStore(db)
}
