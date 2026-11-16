package handler

import (
	"context"

	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type findingCaseReviewApplication interface {
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
