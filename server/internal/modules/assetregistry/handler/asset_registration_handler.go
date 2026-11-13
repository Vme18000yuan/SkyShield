package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type assetRegistrar interface {
	Register(context.Context, assetapp.BatchRegisterInput) (*assetapp.BatchRegisterResult, error)
}

type AssetRegistrationHandler struct{ writer assetRegistrar }

func NewAssetRegistrationHandler(writer assetRegistrar) *AssetRegistrationHandler {
	return &AssetRegistrationHandler{writer: writer}
}

// BatchCreate registers independently atomic asset observations.
// POST /v1/assetEntities:batchCreate
func (h *AssetRegistrationHandler) BatchCreate(c *gin.Context) {
	user, ok := middleware.GetUserID(c)
	if !ok {
		httpdto.Unauthorized(c, "Not authenticated")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2*1024*1024)
	var request struct {
		RequestID string                `json:"requestId"`
		Assets    []assetapp.AssetInput `json:"assets"`
	}
	if !httpdto.BindJSON(c, &request) {
		return
	}
	if request.RequestID != "" {
		parsed, err := uuid.Parse(request.RequestID)
		if err != nil || parsed.String() != request.RequestID {
			httpdto.BadRequest(c, "Invalid requestId")
			return
		}
	}
	result, err := h.writer.Register(c.Request.Context(), assetapp.BatchRegisterInput{UserID: user, Origin: "manual", RequestID: request.RequestID, Assets: request.Assets})
	if err != nil {
		httpdto.BadRequest(c, "Invalid asset registration request")
		return
	}
	httpdto.Success(c, result)
}
