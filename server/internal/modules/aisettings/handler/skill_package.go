package handler

import (
	"github.com/gin-gonic/gin"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
	"net/http"
	"strconv"
	"strings"
)

type SkillPackageHandler struct{ app *settings.SkillPackageFacade }

func NewSkillPackageHandler(app *settings.SkillPackageFacade) *SkillPackageHandler {
	return &SkillPackageHandler{app: app}
}
func (h *SkillPackageHandler) List(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	items, err := h.app.List(c.Request.Context(), user)
	if err != nil {
		skillError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"skills": items})
}
func (h *SkillPackageHandler) Create(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 21<<20)
	var input struct {
		Name          string   `json:"name"`
		Description   string   `json:"description"`
		License       string   `json:"license"`
		Compatibility string   `json:"compatibility"`
		MCPs          []string `json:"mcps"`
		Instructions  string   `json:"instructions"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if err := h.app.Create(c.Request.Context(), user, settings.SkillPackage{Name: input.Name, Description: input.Description, License: input.License, Compatibility: input.Compatibility, MCPs: input.MCPs, Instructions: input.Instructions}); err != nil {
		skillError(c, err)
		return
	}
	httpdto.Created(c, gin.H{"name": input.Name})
}
func (h *SkillPackageHandler) Delete(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	if err := h.app.Delete(c.Request.Context(), user, c.Param("skill")); err != nil {
		skillError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"deleted": c.Param("skill")})
}
func (h *SkillPackageHandler) Metadata(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	var patch settings.SkillMetadataPatch
	if !httpdto.BindJSON(c, &patch) {
		return
	}
	if err := h.app.Metadata(c.Request.Context(), user, c.Param("skill"), patch); err != nil {
		skillError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"ok": true})
}
func (h *SkillPackageHandler) Files(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	files, err := h.app.Files(c.Request.Context(), user, c.Param("skill"))
	if err != nil {
		skillError(c, err)
		return
	}
	paths := []string{}
	for _, file := range files {
		path := file.Path
		if file.Directory {
			path += "/"
		}
		paths = append(paths, path)
	}
	httpdto.Success(c, gin.H{"files": paths})
}
func (h *SkillPackageHandler) File(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	name, path := c.Param("skill"), strings.TrimPrefix(c.Param("file"), "/")
	switch c.Request.Method {
	case http.MethodGet:
		content, err := h.app.Read(c.Request.Context(), user, name, path)
		if err != nil {
			skillError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"file": path, "content": content})
	case http.MethodPut:
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 21<<20)
		var input struct {
			Content string `json:"content"`
		}
		if !httpdto.BindJSON(c, &input) {
			return
		}
		if err := h.app.Write(c.Request.Context(), user, name, settings.SkillFile{Path: path, Content: []byte(input.Content)}); err != nil {
			skillError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"ok": true})
	case http.MethodDelete:
		if err := h.app.DeletePath(c.Request.Context(), user, name, path); err != nil {
			skillError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"deleted": path})
	}
}
func (h *SkillPackageHandler) Directory(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	var input struct {
		Path string `json:"path"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if err := h.app.Write(c.Request.Context(), user, c.Param("skill"), settings.SkillFile{Path: input.Path, Directory: true, Mode: 0755}); err != nil {
		skillError(c, err)
		return
	}
	httpdto.Created(c, gin.H{"dir": input.Path})
}
func (h *SkillPackageHandler) Visibility(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	if c.Request.Method == http.MethodGet {
		agents, err := h.app.Visibility(c.Request.Context(), user, c.Param("skill"))
		if err != nil {
			skillError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"agents": agents})
		return
	}
	var input struct {
		Agent   string `json:"agent"`
		Visible bool   `json:"visible"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if err := h.app.Toggle(c.Request.Context(), user, c.Param("skill"), input.Agent, input.Visible); err != nil {
		skillError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"ok": true})
}
func (h *SkillPackageHandler) Usage(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	items, err := h.app.Usage(c.Request.Context(), user, c.Param("skill"), limit)
	if err != nil {
		skillError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"calls": items})
}
func (h *SkillPackageHandler) Missing(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	items, err := h.app.Missing(c.Request.Context(), user, limit)
	if err != nil {
		skillError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"missing": items})
}
func (h *SkillPackageHandler) Upload(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 21<<20)
	file, err := c.FormFile("file")
	if err != nil {
		httpdto.BadRequest(c, "请选择 ZIP 文件")
		return
	}
	defer c.Request.MultipartForm.RemoveAll()
	reader, err := file.Open()
	if err != nil {
		skillError(c, err)
		return
	}
	defer reader.Close()
	name, count, err := h.app.Upload(c.Request.Context(), user, reader, c.Query("overwrite") == "true")
	if err != nil {
		httpdto.BadRequest(c, err.Error())
		return
	}
	httpdto.Success(c, gin.H{"name": name, "files": count})
}
