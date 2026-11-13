package handler

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type findingReviewBatchApplication interface {
	FindingReviewCandidates(context.Context, int, string, string, int, int) (aiapp.FindingReviewCandidatePage, error)
	CreateCaseReviewBatch(context.Context, int, []aiapp.FindingCaseReviewBatch) ([]aiapp.FindingCaseReview, error)
}

func (h *TaskHandler) FindingReviewCandidates(c *gin.Context) {
	user, ok := conversationUser(c)
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
	app, ok := h.app.(findingReviewBatchApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	result, err := app.FindingReviewCandidates(c.Request.Context(), user, strings.TrimSpace(c.Query("q")), strings.TrimSpace(c.Query("severity")), int(page), int(size))
	if err != nil {
		taskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpdto.Success(c, result)
}

func (h *TaskHandler) CreateFindingCaseReviewBatch(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	app, ok := h.app.(findingReviewBatchApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16384)
	var input struct {
		Batches []struct {
			TaskID     string   `json:"task_id"`
			FindingIDs []string `json:"finding_ids"`
		} `json:"batches"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	batches := make([]aiapp.FindingCaseReviewBatch, 0, len(input.Batches))
	for _, item := range input.Batches {
		taskID, err := strconv.ParseInt(item.TaskID, 10, 64)
		if err != nil || taskID <= 0 {
			taskError(c, aiapp.ErrInvalidInput)
			return
		}
		batch := aiapp.FindingCaseReviewBatch{TaskID: taskID}
		for _, raw := range item.FindingIDs {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 {
				taskError(c, aiapp.ErrInvalidInput)
				return
			}
			batch.FindingIDs = append(batch.FindingIDs, id)
		}
		batches = append(batches, batch)
	}
	result, err := app.CreateCaseReviewBatch(c.Request.Context(), user, batches)
	if err != nil {
		taskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpdto.Success(c, gin.H{"reviews": result})
}
