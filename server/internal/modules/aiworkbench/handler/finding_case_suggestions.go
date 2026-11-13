package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type findingCaseSuggestionApplication interface {
	FindingCaseSuggestions(context.Context, int, int64, int, int) (aiapp.FindingCaseSuggestionPage, error)
	ResolveCaseSuggestion(context.Context, int, int64, int64, bool, []int64) (int64, error)
}

func (h *TaskHandler) FindingCaseSuggestions(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	page, ok := trafficInt(c, "page", 0)
	if !ok {
		return
	}
	size, ok := trafficInt(c, "size", 25)
	if !ok {
		return
	}
	if page < 0 || page > 10000 || size < 1 || size > 100 {
		taskError(c, aiapp.ErrInvalidInput)
		return
	}
	app, ok := h.app.(findingCaseSuggestionApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	result, err := app.FindingCaseSuggestions(c.Request.Context(), user, task, int(page), int(size))
	if err != nil {
		taskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpdto.Success(c, result)
}

func (h *TaskHandler) ResolveFindingCaseSuggestion(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	suggestion, ok := parseCatalogID(c, "suggestion")
	if !ok {
		return
	}
	app, ok := h.app.(findingCaseSuggestionApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var input struct {
		Accept  *bool    `json:"accept"`
		Exclude []string `json:"exclude"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if input.Accept == nil {
		taskError(c, aiapp.ErrInvalidInput)
		return
	}
	ids := make([]int64, 0, len(input.Exclude))
	for _, raw := range input.Exclude {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			taskError(c, aiapp.ErrInvalidInput)
			return
		}
		ids = append(ids, id)
	}
	caseID, err := app.ResolveCaseSuggestion(c.Request.Context(), user, task, suggestion, *input.Accept, ids)
	if err != nil {
		taskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	result := gin.H{"resolved": true}
	if caseID > 0 {
		result["case_id"] = strconv.FormatInt(caseID, 10)
	}
	httpdto.Success(c, result)
}
