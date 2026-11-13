package aiworkbenchwiring

import (
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
)

var _ aiapp.ConversationStore = (*airepo.ConversationStore)(nil)
var _ aiapp.TaskStore = (*airepo.TaskStore)(nil)
