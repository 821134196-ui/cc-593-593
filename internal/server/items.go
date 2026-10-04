package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"lostfound/internal/model"
)

// createItem 保洁登记客房发现的物品：房号、发现时间、照片、封袋号
func (s *Server) createItem(c *gin.Context) {
	staff := currentStaff(c)

	bagNo := strings.TrimSpace(c.PostForm("bag_no"))
	roomNo := strings.TrimSpace(c.PostForm("room_no"))
	category := c.PostForm("category")
	description := c.PostForm("description")
	storageLoc := c.PostForm("storage_loc")
	foundAtStr := c.PostForm("found_at")

	if bagNo == "" || roomNo == "" || foundAtStr == "" {
		respondErr(c, errBiz(http.StatusBadRequest, "missing_field", "封袋号、房号、发现时间为必填"))
		return
	}
	switch category {
	case model.CategoryGeneral, model.CategoryElectronics, model.CategoryDocument, model.CategoryCash, model.CategoryValuable:
	default:
		respondErr(c, errBiz(http.StatusBadRequest, "bad_category", "未知的物品类别"))
		return
	}
	foundAt, err := time.Parse(time.RFC3339, foundAtStr)
	if err != nil {
		if t, err2 := time.Parse("2006-01-02 15:04", foundAtStr); err2 == nil {
			foundAt = t
		} else {
			respondErr(c, errBiz(http.StatusBadRequest, "bad_time", "发现时间格式应为 RFC3339 或 2006-01-02 15:04"))
			return
		}
	}
	if storageLoc == "" {
		storageLoc = "前台保管柜"
	}

	item := model.Item{
		BagNo:       bagNo,
		RoomNo:      roomNo,
		FoundAt:     foundAt,
		Category:    category,
		Description: description,
		StorageLoc:  storageLoc,
		Status:      model.StatusStored,
		RetainUntil: foundAt.AddDate(0, 0, s.retentionDays),
		CreatedByID: staff.ID,
	}

	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&item).Error; err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return errBiz(http.StatusConflict, "duplicate_bag_no", "封袋号已存在，请勿重复登记")
			}
			return err
		}
		photos, err := s.saveUploadedPhotos(c, item.ID)
		if err != nil {
			return err
		}
		for i := range photos {
			if err := tx.Create(&photos[i]).Error; err != nil {
				return err
			}
		}
		return appendHandover(tx, item.ID, model.ActRegister, staff.ID, "", "登记入库，保管位置："+storageLoc)
	})
	if err != nil {
		respondErr(c, err)
		return
	}
	s.db.Preload("Photos").Preload("CreatedBy").First(&item, item.ID)
	c.JSON(http.StatusCreated, maskedItem(&item))
}

// listItems 前台按住客提供的描述搜索；证件/现金只显示必要信息
func (s *Server) listItems(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	roomNo := strings.TrimSpace(c.Query("room_no"))
	status := strings.TrimSpace(c.Query("status"))

	db := s.db.Model(&model.Item{}).Preload("Photos").Order("found_at DESC")
	if roomNo != "" {
		db = db.Where("room_no = ?", roomNo)
	}
	if status != "" {
		db = db.Where("status = ?", status)
	}
	if q != "" {
		like := "%" + q + "%"
		db = db.Where("description LIKE ? OR bag_no LIKE ? OR room_no LIKE ?", like, like, like)
	}
	var items []model.Item
	if err := db.Find(&items).Error; err != nil {
		respondErr(c, err)
		return
	}
	out := make([]gin.H, 0, len(items))
	for i := range items {
		out = append(out, maskedItem(&items[i]))
	}
	c.JSON(200, out)
}

// getItem 物品详情：时间线（保管位置、经手员工、状态）+ 认领记录
// 敏感物品的完整描述与照片仅前台/经理可见
func (s *Server) getItem(c *gin.Context) {
	var item model.Item
	if err := s.db.Preload("Photos").Preload("CreatedBy").First(&item, c.Param("id")).Error; err != nil {
		respondErr(c, errItemNotFound)
		return
	}

	staff := s.optionalStaff(c)
	canSeeSensitive := staff != nil && (staff.Role == "frontdesk" || staff.Role == "manager")

	view := maskedItem(&item)
	if !item.Sensitive() || canSeeSensitive {
		view["description"] = item.Description
		view["storage_loc"] = item.StorageLoc
		photos := []string{}
		for _, p := range item.Photos {
			photos = append(photos, p.Path)
		}
		view["photos"] = photos
	}
	view["retain_until"] = item.RetainUntil
	view["created_by"] = item.CreatedBy.Name

	var handovers []model.Handover
	s.db.Preload("Staff").Where("item_id = ?", item.ID).Order("created_at ASC, id ASC").Find(&handovers)

	var claims []model.Claim
	s.db.Preload("Authorization").Where("item_id = ?", item.ID).Order("created_at ASC").Find(&claims)
	claimViews := make([]gin.H, 0, len(claims))
	for i := range claims {
		claimViews = append(claimViews, maskedClaim(&claims[i]))
	}

	c.JSON(200, gin.H{
		"item":      view,
		"timeline":  handovers,
		"claims":    claimViews,
		"delivered": item.Status == model.StatusDelivered || item.Status == model.StatusShipped,
	})
}

func (s *Server) listHandovers(c *gin.Context) {
	var handovers []model.Handover
	s.db.Preload("Staff").Where("item_id = ?", c.Param("id")).Order("created_at ASC, id ASC").Find(&handovers)
	c.JSON(200, handovers)
}

// moveItem 变更保管位置（如移交库房）
func (s *Server) moveItem(c *gin.Context) {
	staff := currentStaff(c)
	var req struct {
		StorageLoc string `json:"storage_loc" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondErr(c, errBiz(http.StatusBadRequest, "missing_field", "保管位置必填"))
		return
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var item model.Item
		if err := tx.First(&item, c.Param("id")).Error; err != nil {
			return errItemNotFound
		}
		if item.Status == model.StatusDelivered || item.Status == model.StatusShipped {
			return errBiz(http.StatusConflict, "already_out", "物品已出库，不能变更位置")
		}
		if err := tx.Model(&item).Update("storage_loc", req.StorageLoc).Error; err != nil {
			return err
		}
		return appendHandover(tx, item.ID, model.ActMove, staff.ID, "", "保管位置变更："+item.StorageLoc+" → "+req.StorageLoc)
	})
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// resumeItem 经理复核后恢复可交付
func (s *Server) resumeItem(c *gin.Context) {
	staff := currentStaff(c)
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var item model.Item
		if err := tx.First(&item, c.Param("id")).Error; err != nil {
			return errItemNotFound
		}
		if item.Status != model.StatusHold {
			return errBiz(http.StatusConflict, "not_on_hold", "物品不在暂停交付状态")
		}
		if err := tx.Model(&item).Updates(map[string]any{"status": model.StatusStored, "hold_reason": ""}).Error; err != nil {
			return err
		}
		return appendHandover(tx, item.ID, model.ActResume, staff.ID, "", "经理复核，恢复可交付")
	})
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}
