package server

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"lostfound/internal/model"
	"lostfound/internal/notify"
)

// 业务错误：code 用于前端/测试断言，http 状态与之一一对应
type bizError struct {
	Status int
	Code   string
	Msg    string
}

func (e *bizError) Error() string { return e.Msg }

func errBiz(status int, code, msg string) *bizError {
	return &bizError{Status: status, Code: code, Msg: msg}
}

var (
	errItemNotFound   = errBiz(http.StatusNotFound, "item_not_found", "物品不存在")
	errClaimNotFound  = errBiz(http.StatusNotFound, "claim_not_found", "认领单不存在")
	errStaffRequired  = errBiz(http.StatusUnauthorized, "staff_required", "缺少有效的员工身份（X-Staff-Id）")
	errForbidden      = errBiz(http.StatusForbidden, "forbidden", "当前员工角色无权执行此操作")
	errNotDeliverable = errBiz(http.StatusConflict, "not_deliverable", "物品当前状态不可交付")
	errConcurrent     = errBiz(http.StatusConflict, "concurrent_checkout", "物品已被其他员工出库，本次操作取消")
)

type Server struct {
	db            *gorm.DB
	photoDir      string
	notifier      *notify.Notifier
	retentionDays int
}

func New(db *gorm.DB, photoDir string, notifier *notify.Notifier, retentionDays int) *Server {
	return &Server{db: db, photoDir: photoDir, notifier: notifier, retentionDays: retentionDays}
}

func (s *Server) Router(staticDir string) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())

	api := r.Group("/api")
	{
		api.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
		api.GET("/staff", s.listStaff)

		api.POST("/items", s.staffRequired("housekeeper", "frontdesk", "manager"), s.createItem)
		api.GET("/items", s.listItems)
		api.GET("/items/:id", s.getItem)
		api.GET("/items/:id/handovers", s.listHandovers)
		api.POST("/items/:id/move", s.staffRequired("housekeeper", "frontdesk", "manager"), s.moveItem)
		api.POST("/items/:id/resume", s.staffRequired("manager"), s.resumeItem)
		api.POST("/items/:id/checkout", s.staffRequired("frontdesk", "manager"), s.checkout)
		api.POST("/items/:id/ship", s.staffRequired("frontdesk", "manager"), s.ship)

		api.POST("/items/:id/claims", s.staffRequired("frontdesk", "manager"), s.createClaim)
		api.POST("/claims/:id/verify", s.staffRequired("frontdesk", "manager"), s.verifyClaim)
		api.POST("/claims/:id/authorization", s.staffRequired("frontdesk", "manager"), s.createAuthorization)
		api.POST("/claims/:id/authorization/revoke", s.staffRequired("frontdesk", "manager"), s.revokeAuthorization)
	}

	// 照片与前端静态资源
	r.Static("/photos", s.photoDir)
	if staticDir != "" {
		r.Static("/assets", filepath.Join(staticDir, "assets"))
		r.NoRoute(func(c *gin.Context) {
			if strings.HasPrefix(c.Request.URL.Path, "/api/") {
				c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
				return
			}
			c.File(filepath.Join(staticDir, "index.html"))
		})
	}
	return r
}

// staffRequired 简化员工鉴权：请求头 X-Staff-Id 标识当前操作员工
func (s *Server) staffRequired(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.GetHeader("X-Staff-Id")
		id, err := strconv.Atoi(idStr)
		if err != nil || id <= 0 {
			c.AbortWithStatusJSON(errStaffRequired.Status, gin.H{"error": errStaffRequired.Code, "message": errStaffRequired.Msg})
			return
		}
		var st model.Staff
		if err := s.db.First(&st, id).Error; err != nil {
			c.AbortWithStatusJSON(errStaffRequired.Status, gin.H{"error": errStaffRequired.Code, "message": errStaffRequired.Msg})
			return
		}
		ok := false
		for _, r := range roles {
			if st.Role == r {
				ok = true
				break
			}
		}
		if !ok {
			c.AbortWithStatusJSON(errForbidden.Status, gin.H{"error": errForbidden.Code, "message": errForbidden.Msg})
			return
		}
		c.Set("staff", &st)
		c.Next()
	}
}

func currentStaff(c *gin.Context) *model.Staff {
	if v, ok := c.Get("staff"); ok {
		return v.(*model.Staff)
	}
	return nil
}

// optionalStaff 用于详情接口：敏感物品仅前台/经理可见完整信息
func (s *Server) optionalStaff(c *gin.Context) *model.Staff {
	idStr := c.GetHeader("X-Staff-Id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		return nil
	}
	var st model.Staff
	if err := s.db.First(&st, id).Error; err != nil {
		return nil
	}
	return &st
}

func respondErr(c *gin.Context, err error) {
	var be *bizError
	if errors.As(err, &be) {
		c.JSON(be.Status, gin.H{"error": be.Code, "message": be.Msg})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal", "message": err.Error()})
}

func (s *Server) listStaff(c *gin.Context) {
	var staff []model.Staff
	s.db.Order("id").Find(&staff)
	c.JSON(200, staff)
}

// appendHandover 交接记录只追加
func appendHandover(tx *gorm.DB, itemID uint, action string, staffID uint, counterparty, note string) error {
	h := model.Handover{
		ItemID:       itemID,
		Action:       action,
		StaffID:      staffID,
		Counterparty: counterparty,
		Note:         note,
		CreatedAt:    time.Now(),
	}
	return tx.Create(&h).Error
}

// ---- 脱敏 ----

func maskIDNo(s string) string {
	if len(s) <= 5 {
		return "***"
	}
	return s[:3] + strings.Repeat("*", len(s)-5) + s[len(s)-2:]
}

func maskPhone(s string) string {
	if len(s) < 7 {
		return "***"
	}
	return s[:3] + "****" + s[len(s)-4:]
}

// maskedItem 普通搜索视图：敏感物品只保留必要信息
func maskedItem(i *model.Item) gin.H {
	out := gin.H{
		"id":          i.ID,
		"bag_no":      i.BagNo,
		"room_no":     i.RoomNo,
		"found_at":    i.FoundAt,
		"category":    i.Category,
		"status":      i.Status,
		"hold_reason": i.HoldReason,
		"sensitive":   i.Sensitive(),
		"created_at":  i.CreatedAt,
	}
	if i.Sensitive() {
		out["description"] = "敏感物品，详情需前台核验身份后查看"
		out["storage_loc"] = "保密柜"
		out["photos"] = []any{}
	} else {
		out["description"] = i.Description
		out["storage_loc"] = i.StorageLoc
		photos := []string{}
		for _, p := range i.Photos {
			photos = append(photos, p.Path)
		}
		out["photos"] = photos
	}
	return out
}

func maskedClaim(cl *model.Claim) gin.H {
	out := gin.H{
		"id":           cl.ID,
		"item_id":      cl.ItemID,
		"claimant":     cl.ClaimantName,
		"id_no":        maskIDNo(cl.ClaimantIDNo),
		"phone":        maskPhone(cl.Phone),
		"description":  cl.Description,
		"pickup_type":  cl.PickupType,
		"status":       cl.Status,
		"reject_reason": cl.RejectReason,
		"verified_at":  cl.VerifiedAt,
		"created_at":   cl.CreatedAt,
	}
	if cl.Authorization != nil {
		out["authorization"] = gin.H{
			"id":           cl.Authorization.ID,
			"grantor_name": cl.Authorization.GrantorName,
			"proxy_name":   cl.Authorization.ProxyName,
			"proxy_id_no":  maskIDNo(cl.Authorization.ProxyIDNo),
			"status":       cl.Authorization.Status,
			"created_at":   cl.Authorization.CreatedAt,
			"revoked_at":   cl.Authorization.RevokedAt,
		}
	}
	return out
}

// saveUploadedPhotos 保存上传的照片到本地目录
func (s *Server) saveUploadedPhotos(c *gin.Context, itemID uint) ([]model.Photo, error) {
	form, err := c.MultipartForm()
	if err != nil {
		return nil, nil // 无文件时按无照片处理
	}
	files := form.File["photos"]
	var photos []model.Photo
	for idx, fh := range files {
		if fh.Size > 10<<20 {
			return nil, errBiz(http.StatusBadRequest, "photo_too_large", "单张照片不能超过 10MB")
		}
		ext := strings.ToLower(filepath.Ext(fh.Filename))
		if ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".webp" {
			return nil, errBiz(http.StatusBadRequest, "bad_photo_type", "仅支持 jpg/png/webp 照片")
		}
		name := fmt.Sprintf("item%d_%d_%d%s", itemID, time.Now().UnixNano(), idx, ext)
		dst := filepath.Join(s.photoDir, name)
		if err := c.SaveUploadedFile(fh, dst); err != nil {
			return nil, err
		}
		photos = append(photos, model.Photo{ItemID: itemID, Path: "/photos/" + name})
	}
	return photos, nil
}

func ensureDir(dir string) error {
	return os.MkdirAll(dir, 0o755)
}
