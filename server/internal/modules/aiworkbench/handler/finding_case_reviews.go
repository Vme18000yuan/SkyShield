package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type findingCaseReviewApplication interface {
	CreateCaseReview(context.Context, int, int64, []int64) (*aiapp.FindingCaseReview, error)
	CaseReviews(context.Context, int, int64, int64, int, int) (aiapp.FindingCaseReviewPage, error)
	CancelQueuedCaseReview(context.Context, int, int64, int64) error
}

func (h *TaskHandler) CancelFindingCaseReview(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	review, ok := parseCatalogID(c, "review")
	if !ok {
		return
	}
	app, ok := h.app.(findingCaseReviewApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	if err := app.CancelQueuedCaseReview(c.Request.Context(), user, task, review); err != nil {
		taskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpdto.Success(c, gin.H{"cancelled": true})
}

func (h *TaskHandler) FindingCaseReviews(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	app, ok := h.app.(findingCaseReviewApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	c.Header("Cache-Control", "no-store")
	if c.Request.Method == http.MethodPost {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
		var input struct {
			FindingIDs []string `json:"finding_ids"`
		}
		if !httpdto.BindJSON(c, &input) {
			return
		}
		ids := make([]int64, 0, len(input.FindingIDs))
		for _, raw := range input.FindingIDs {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 {
				taskError(c, aiapp.ErrInvalidInput)
				return
			}
			ids = append(ids, id)
		}
		review, err := app.CreateCaseReview(c.Request.Context(), user, task, ids)
		if err != nil {
			taskError(c, err)
			return
		}
		httpdto.Success(c, review)
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
	var reviewID int64
	if c.Param("review") != "" {
		reviewID, ok = parseCatalogID(c, "review")
		if !ok {
			return
		}
		page, size = 0, 1
	}
	if page < 0 || page > 10000 || size < 1 || size > 100 {
		taskError(c, aiapp.ErrInvalidInput)
		return
	}
	result, err := app.CaseReviews(c.Request.Context(), user, task, reviewID, int(page), int(size))
	if err != nil {
		taskError(c, err)
		return
	}
	if reviewID > 0 {
		if len(result.Items) != 1 {
			taskError(c, aiapp.ErrTaskNotFound)
			return
		}
		httpdto.Success(c, result.Items[0])
		return
	}
	httpdto.Success(c, result)
}
