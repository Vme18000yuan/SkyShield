package aisettingswiring

import (
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	settingsrepo "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
)

var _ settingsapp.ProfileStore = (*settingsrepo.LLMProfileStore)(nil)
