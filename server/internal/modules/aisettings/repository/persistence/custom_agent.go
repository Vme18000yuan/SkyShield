package persistence

import "time"

type CustomAgent struct {
	TenantID     string    `gorm:"column:tenant_id;type:uuid;primaryKey"`
	AgentKey     string    `gorm:"column:agent_key;primaryKey"`
	DisplayName  string    `gorm:"column:display_name;type:varchar(120);not null"`
	Description  string    `gorm:"column:description;not null"`
	SystemPrompt string    `gorm:"column:system_prompt;not null"`
	Enabled      bool      `gorm:"column:enabled;not null"`
	MaxTurns     int       `gorm:"column:max_turns;not null"`
	RunSeconds   int       `gorm:"column:run_seconds;not null"`
	CreatedAt    time.Time `gorm:"column:created_at;type:timestamptz;not null"`
	UpdatedAt    time.Time `gorm:"column:updated_at;type:timestamptz;not null"`
}

func (CustomAgent) TableName() string { return "ai_custom_agent" }
