package handler

import (
	"context"
	"strconv"

	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type coverageGraphApplication interface {
	CoverageGraph(context.Context, int, int64) (aiapp.CoverageGraph, error)
	AssetRefs(context.Context, int, int64, int64) (aiapp.CoverageAssetRefs, error)
}

func (h *TaskHandler) CoverageGraph(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	app, ok := h.app.(coverageGraphApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	graph, err := app.CoverageGraph(c.Request.Context(), user, task)
	if err != nil {
		taskError(c, err)
		return
	}
	httpdto.Success(c, graph)
}
func (h *TaskHandler) AssetRefs(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	asset, err := strconv.ParseInt(c.Query("asset_id"), 10, 64)
	if err != nil || asset <= 0 {
		httpdto.BadRequest(c, "Invalid asset ID")
		return
	}
	app, ok := h.app.(coverageGraphApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	refs, err := app.AssetRefs(c.Request.Context(), user, task, asset)
	if err != nil {
		taskError(c, err)
		return
	}
	httpdto.Success(c, refs)
}
