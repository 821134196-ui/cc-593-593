package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"hotel-lostfound/internal/auth"
	"hotel-lostfound/internal/domain"
	"hotel-lostfound/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Handler struct {
	svc       *service.Service
	authMgr   *auth.Manager
	db        *gorm.DB
	uploadDir string
}

func NewHandler(svc *service.Service, authMgr *auth.Manager, db *gorm.DB, uploadDir string) *Handler {
	return &Handler{svc: svc, authMgr: authMgr, db: db, uploadDir: uploadDir}
}

func (h *Handler) Register(r *gin.Engine) {
	api := r.Group("/api")
	api.POST("/auth/login", h.login)
	api.POST("/auth/logout", h.authMgr.Middleware(), h.logout)
	api.GET("/auth/me", h.authMgr.Middleware(), func(c *gin.Context) {
		emp := auth.CurrentUser(c)
		c.JSON(http.StatusOK, gin.H{"id": emp.ID, "username": emp.Username, "full_name": emp.FullName, "role": emp.Role})
	})

	authed := api.Group("", h.authMgr.Middleware())
	{
		authed.GET("/items", h.listItems)
		authed.GET("/items/:id", h.getItem)
		authed.GET("/items/:id/detail", h.getItem) // 别名
		authed.GET("/photos/:name", h.servePhoto)
		authed.POST("/items/:id/move", h.moveItem)
		authed.GET("/items/:id/notifications", h.listNotifications)
	}

	// 保洁：发现登记（含照片上传）
	house := authed.Group("", auth.RequireRole(domain.RoleHousekeeping))
	{
		house.POST("/items", h.registerItem)
	}

	// 前台：认领登记、身份核实、授权、出库交付
	desk := authed.Group("", auth.RequireRole(domain.RoleFrontDesk))
	{
		desk.POST("/items/:id/claims", h.fileClaim)
		desk.POST("/claims/:id/verify", h.verifyClaim)
		desk.POST("/claims/:id/deliver", h.deliverForClaim)
		desk.POST("/claims/:id/authorization", h.createAuth)
		desk.POST("/authorizations/:id/revoke", h.revokeAuth)
		desk.POST("/items/:id/release", h.releaseItem)
		desk.POST("/holds/:id/resolve", h.resolveHold)
	}
}

// ---------------- 认证 ----------------

func (h *Handler) login(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	tok, emp, ok := h.authMgr.Login(req.Username, req.Password)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": tok, "user": gin.H{
		"id": emp.ID, "username": emp.Username, "full_name": emp.FullName, "role": emp.Role,
	}})
}

func (h *Handler) logout(c *gin.Context) {
	tok := c.GetHeader("Authorization")
	if len(tok) > 7 {
		h.authMgr.Logout(tok[7:])
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------------- 物品 ----------------

func (h *Handler) registerItem(c *gin.Context) {
	emp := auth.CurrentUser(c)
	// multipart 表单：房号、时间、描述、必要信息、分类、封袋号、位置、保管天数 + 照片文件
	room := c.PostForm("room_number")
	desc := c.PostForm("description")
	summary := c.PostForm("public_summary")
	category := c.PostForm("category")
	seal := c.PostForm("bag_seal_number")
	location := c.PostForm("storage_location")
	days, _ := strconv.Atoi(c.PostForm("retention_days"))
	foundAt := time.Now()
	if s := c.PostForm("found_at"); s != "" {
		if t, err := time.ParseInLocation("2006-01-02T15:04", s, time.Local); err == nil {
			foundAt = t
		} else if t, err := time.Parse("2006-01-02 15:04", s); err == nil {
			foundAt = t
		}
	}

	var photoName string
	if fh, err := c.FormFile("photo"); err == nil {
		ext := filepath.Ext(fh.Filename)
		if ext == "" {
			ext = ".jpg"
		}
		photoName = uuid.NewString() + ext
		if err := c.SaveUploadedFile(fh, filepath.Join(h.uploadDir, photoName)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "照片保存失败: " + err.Error()})
			return
		}
	}

	if room == "" || desc == "" || seal == "" || location == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "房号、描述、封袋号、保管位置为必填项"})
		return
	}
	if category == "" {
		category = "other"
	}

	item, err := h.svc.RegisterItem(service.RegisterInput{
		RoomNumber:      room,
		FoundAt:         foundAt,
		Description:     desc,
		PublicSummary:   summary,
		Category:        category,
		BagSealNumber:   seal,
		StorageLocation: location,
		RetentionDays:   days,
		PhotoPath:       photoName,
	}, emp)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"item": item})
}

func (h *Handler) listItems(c *gin.Context) {
	q := c.Query("q")
	category := c.Query("category")
	views, err := h.svc.Search(q, category)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	emp := auth.CurrentUser(c)
	c.JSON(http.StatusOK, gin.H{"items": views, "as_role": emp.Role})
}

func (h *Handler) getItem(c *gin.Context) {
	d, err := h.svc.GetItem(c.Param("id"))
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "物品不存在"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 证件 / 现金的完整详情仅前台在认领核实环节可见，其他角色只给必要信息
	emp := auth.CurrentUser(c)
	if emp.Role != domain.RoleFrontDesk {
		if d.Item.Sensitivity != domain.SensitivityNormal {
			d.Item.Description = d.Item.PublicSummary
		}
		for i := range d.Claims {
			d.Claims[i].Claimant.IDNumber = ""
		}
	}
	c.JSON(http.StatusOK, d)
}

func (h *Handler) moveItem(c *gin.Context) {
	var req struct {
		Location string `json:"location"`
	}
	_ = c.ShouldBindJSON(&req)
	if req.Location == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "location 必填"})
		return
	}
	if err := h.svc.MoveLocation(c.Param("id"), req.Location, auth.CurrentUser(c)); err != nil {
		writeServiceErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------------- 认领 / 核实 ----------------

func (h *Handler) fileClaim(c *gin.Context) {
	var req struct {
		FullName    string `json:"full_name"`
		Phone       string `json:"phone"`
		IDType      string `json:"id_type"`
		IDNumber    string `json:"id_number"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.FullName == "" || req.Description == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "姓名与物品描述必填"})
		return
	}
	claim, err := h.svc.FileClaim(c.Param("id"), domain.Claimant{
		FullName: req.FullName, Phone: req.Phone, IDType: req.IDType, IDNumber: req.IDNumber,
	}, req.Description)
	if err != nil {
		writeServiceErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"claim": claim})
}

func (h *Handler) verifyClaim(c *gin.Context) {
	var req struct {
		Approved bool   `json:"approved"`
		Note     string `json:"note"`
	}
	_ = c.ShouldBindJSON(&req)
	if err := h.svc.VerifyClaim(c.Param("id"), req.Approved, req.Note, auth.CurrentUser(c)); err != nil {
		writeServiceErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// deliverForClaim 以本人自取方式交付指定认领记录
func (h *Handler) deliverForClaim(c *gin.Context) {
	var req struct {
		SealPresented string `json:"seal_presented"`
	}
	_ = c.ShouldBindJSON(&req)
	var claim domain.Claim
	if err := h.db.First(&claim, "id = ?", c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "认领记录不存在"})
		return
	}
	res, err := h.svc.Release(service.ReleaseInput{
		ItemID:        claim.ItemID,
		ClaimID:       c.Param("id"),
		Method:        domain.DeliverySelfPickup,
		SealPresented: req.SealPresented,
	}, auth.CurrentUser(c))
	if err != nil {
		writeServiceErr(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

// ---------------- 授权 ----------------

func (h *Handler) createAuth(c *gin.Context) {
	var req struct {
		ProxyName     string `json:"proxy_name"`
		ProxyIDType   string `json:"proxy_id_type"`
		ProxyIDNumber string `json:"proxy_id_number"`
		TTLHours      int    `json:"ttl_hours"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.ProxyName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "代领人姓名必填"})
		return
	}
	if req.TTLHours <= 0 {
		req.TTLHours = 72
	}
	auth2, err := h.svc.CreateAuthorization(c.Param("id"), req.ProxyName, req.ProxyIDType, req.ProxyIDNumber,
		time.Duration(req.TTLHours)*time.Hour, auth.CurrentUser(c))
	if err != nil {
		writeServiceErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"authorization": auth2})
}

func (h *Handler) revokeAuth(c *gin.Context) {
	if err := h.svc.RevokeAuthorization(c.Param("id"), auth.CurrentUser(c)); err != nil {
		writeServiceErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------------- 出库 ----------------

func (h *Handler) releaseItem(c *gin.Context) {
	var req service.ReleaseInput
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.ItemID = c.Param("id")
	res, err := h.svc.Release(req, auth.CurrentUser(c))
	if err != nil {
		writeServiceErr(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

func (h *Handler) resolveHold(c *gin.Context) {
	var req struct {
		Note       string `json:"note"`
		ExtendDays int    `json:"extend_days"`
	}
	_ = c.ShouldBindJSON(&req)
	if err := h.svc.ResolveHold(c.Param("id"), req.Note, req.ExtendDays, auth.CurrentUser(c)); err != nil {
		writeServiceErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) listNotifications(c *gin.Context) {
	var ns []domain.ShippingNotification
	h.db.Order("created_at DESC").Find(&ns, "item_id = ?", c.Param("id"))
	c.JSON(http.StatusOK, gin.H{"notifications": ns})
}

// ---------------- 照片 ----------------

func (h *Handler) servePhoto(c *gin.Context) {
	name := filepath.Base(c.Param("name"))
	p := filepath.Join(h.uploadDir, name)
	if _, err := os.Stat(p); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "照片不存在"})
		return
	}
	c.File(p)
}

// ---------------- 辅助 ----------------

func writeServiceErr(c *gin.Context, err error) {
	var ce *service.ConflictError
	if errors.As(err, &ce) {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	if errors.Is(err, service.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}
