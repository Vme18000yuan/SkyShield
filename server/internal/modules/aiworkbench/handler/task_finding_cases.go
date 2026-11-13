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

type taskFindingCaseApplication interface {
	FindingCasesFiltered(context.Context, int, int64, aiapp.TaskFindingCaseFilter) (aiapp.TaskFindingCasePage, error)
	MergeFindingCase(context.Context, int, int64, []int64, string, string) (int64, error)
	DetachFindingCaseMember(context.Context, int, int64, int64, int64, string) error
	SaveFindingCaseReport(context.Context, int, int64, int64, aiapp.FindingCaseReport) error
}

func (h *TaskHandler) FindingCases(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	app, ok := h.app.(taskFindingCaseApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	c.Header("Cache-Control", "no-store")
	if c.Request.Method == http.MethodPost {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16384)
		var input struct {
			FindingIDs []string `json:"finding_ids"`
			Title      string   `json:"title"`
			Reason     string   `json:"reason"`
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
		id, err := app.MergeFindingCase(c.Request.Context(), user, task, ids, input.Title, input.Reason)
		if err != nil {
			taskError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"case_id": strconv.FormatInt(id, 10)})
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
	var caseID int64
	if c.Param("case") != "" {
		caseID, ok = parseCatalogID(c, "case")
		if !ok {
			return
		}
		page, size = 0, 1
	}
	if page < 0 || page > 10000 || size < 1 || size > 100 {
		taskError(c, aiapp.ErrInvalidInput)
		return
	}
	filter := aiapp.TaskFindingCaseFilter{CaseID: caseID, Page: int(page), Size: int(size), Query: strings.TrimSpace(c.Query("q")), Severity: strings.TrimSpace(c.Query("severity"))}
	result, err := app.FindingCasesFiltered(c.Request.Context(), user, task, filter)
	if err != nil {
		taskError(c, err)
		return
	}
	if caseID > 0 {
		if len(result.Items) != 1 {
			taskError(c, aiapp.ErrTaskNotFound)
			return
		}
		httpdto.Success(c, result.Items[0])
		return
	}
	httpdto.Success(c, result)
}

func (h *TaskHandler) DetachFindingCaseMember(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	caseID, ok := parseCatalogID(c, "case")
	if !ok {
		return
	}
	finding, ok := parseCatalogID(c, "finding")
	if !ok {
		return
	}
	app, ok := h.app.(taskFindingCaseApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 10000)
	var input struct {
		Reason string `json:"reason"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if err := app.DetachFindingCaseMember(c.Request.Context(), user, task, caseID, finding, input.Reason); err != nil {
		taskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpdto.Success(c, gin.H{"updated": true})
}

func (h *TaskHandler) FindingCaseReport(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	caseID, ok := parseCatalogID(c, "case")
	if !ok {
		return
	}
	app, ok := h.app.(taskFindingCaseApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 820000)
	var input aiapp.FindingCaseReport
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if err := app.SaveFindingCaseReport(c.Request.Context(), user, task, caseID, input); err != nil {
		taskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpdto.Success(c, gin.H{"updated": true})
}
