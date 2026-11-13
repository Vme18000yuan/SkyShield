package infogather

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type Handler struct{ store *Store }

func NewHandler(store *Store) *Handler { return &Handler{store: store} }

func (h *Handler) Register(routes *gin.RouterGroup) {
	routes.GET("/aiInfoGatherRuns", h.runs)
	routes.GET("/aiInfoGatherRuns/:run/records", h.records)
	routes.POST("/aiInfoGatherRuns/:run/sync", h.sync)
	routes.POST("/aiInfoGatherRuns/:run/ignore", h.ignore)
}

func (h *Handler) ignore(c *gin.Context) {
	user, ok := userID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10)
	var input struct {
		Records []RecordRef `json:"records"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if len(input.Records) == 0 || len(input.Records) > 100 {
		httpdto.BadRequest(c, "请选择 1 至 100 条待处理记录")
		return
	}
	for _, ref := range input.Records {
		if (ref.Kind != "asset" && ref.Kind != "vulnerability" && ref.Kind != "observation") || !recordIDPattern.MatchString(ref.ID) {
			httpdto.BadRequest(c, "记录无效")
			return
		}
	}
	count, err := h.store.IgnoreSelected(c.Request.Context(), user, c.Param("run"), input.Records)
	if err != nil {
		httpError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"processed": count})
}

func userID(c *gin.Context) (int, bool) {
	id, ok := middleware.GetUserID(c)
	if !ok {
		httpdto.Unauthorized(c, "Not authenticated")
	}
	return id, ok
}
func httpError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		httpdto.Forbidden(c, "当前账号无信息收集权限")
	case errors.Is(err, ErrNotFound):
		httpdto.NotFound(c, "信息收集记录不存在")
	default:
		httpdto.InternalError(c, "信息收集操作失败，请稍后重试")
	}
}

func (h *Handler) runs(c *gin.Context) {
	user, ok := userID(c)
	if !ok {
		return
	}
	items, err := h.store.Runs(c.Request.Context(), user)
	if err != nil {
		httpError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"runs": items})
}

func (h *Handler) records(c *gin.Context) {
	user, ok := userID(c)
	if !ok {
		return
	}
	offset := 0
	if raw := c.Query("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || value > 1000000 {
			httpdto.BadRequest(c, "分页参数无效")
			return
		}
		offset = value
	}
	items, total, err := h.store.Records(c.Request.Context(), user, c.Param("run"), offset)
	if err != nil {
		httpError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"records": items, "total": total, "offset": offset, "pageSize": 100})
}

func (h *Handler) sync(c *gin.Context) {
	user, ok := userID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10)
	var input struct {
		TargetID int         `json:"targetId"`
		Records  []RecordRef `json:"records"`
		AllowCDN bool        `json:"allowCdn"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if input.TargetID <= 0 || len(input.Records) == 0 || len(input.Records) > 100 {
		httpdto.BadRequest(c, "请选择目标及 1 至 100 条记录")
		return
	}
	for _, ref := range input.Records {
		if (ref.Kind != "asset" && ref.Kind != "vulnerability" && ref.Kind != "observation") || !recordIDPattern.MatchString(ref.ID) {
			httpdto.BadRequest(c, "记录无效")
			return
		}
	}
	result, err := h.store.SyncSelected(c.Request.Context(), user, c.Param("run"), input.TargetID, input.Records, input.AllowCDN)
	if err != nil {
		httpError(c, err)
		return
	}
	httpdto.Success(c, result)
}
