package server

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"lostfound/internal/model"
)

// createClaim 找到疑似物品后登记认领人；一件物品可有多个认领人，分别核验
func (s *Server) createClaim(c *gin.Context) {
	var req struct {
		ClaimantName string `json:"claimant_name" binding:"required"`
		ClaimantIDNo string `json:"claimant_id_no" binding:"required"`
		Phone        string `json:"phone"`
		Description  string `json:"description" binding:"required"`
		PickupType   string `json:"pickup_type" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondErr(c, errBiz(http.StatusBadRequest, "missing_field", "认领人姓名、证件号、物品描述、领取方式为必填"))
		return
	}
	switch req.PickupType {
	case model.PickupSelf, model.PickupProxy, model.PickupShip:
	default:
		respondErr(c, errBiz(http.StatusBadRequest, "bad_pickup_type", "领取方式应为 self/proxy/ship"))
		return
	}

	var claim model.Claim
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var item model.Item
		if err := tx.First(&item, c.Param("id")).Error; err != nil {
			return errItemNotFound
		}
		if item.Status == model.StatusDelivered || item.Status == model.StatusShipped {
			return errBiz(http.StatusConflict, "already_delivered", "物品已交付，不再受理新的认领")
		}
		claim = model.Claim{
			ItemID:       item.ID,
			ClaimantName: req.ClaimantName,
			ClaimantIDNo: req.ClaimantIDNo,
			Phone:        req.Phone,
			Description:  req.Description,
			PickupType:   req.PickupType,
			Status:       model.ClaimPending,
		}
		return tx.Create(&claim).Error
	})
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, maskedClaim(&claim))
}

// verifyClaim 前台核实失主身份；多人认领同一件物品时分别验证
func (s *Server) verifyClaim(c *gin.Context) {
	staff := currentStaff(c)
	var req struct {
		Approve      bool   `json:"approve"`
		RejectReason string `json:"reject_reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondErr(c, errBiz(http.StatusBadRequest, "missing_field", "缺少核验结论"))
		return
	}

	err := s.db.Transaction(func(tx *gorm.DB) error {
		var claim model.Claim
		if err := tx.First(&claim, c.Param("id")).Error; err != nil {
			return errClaimNotFound
		}
		var item model.Item
		if err := tx.First(&item, claim.ItemID).Error; err != nil {
			return errItemNotFound
		}
		if item.Status == model.StatusDelivered || item.Status == model.StatusShipped {
			return errBiz(http.StatusConflict, "already_delivered", "物品已交付，不能再核验新的认领人")
		}
		if claim.Status != model.ClaimPending {
			return errBiz(http.StatusConflict, "already_verified", "该认领单已核验，请勿重复操作")
		}
		now := time.Now()
		updates := map[string]any{"verified_by_id": staff.ID, "verified_at": &now}
		if req.Approve {
			updates["status"] = model.ClaimVerified
		} else {
			if req.RejectReason == "" {
				return errBiz(http.StatusBadRequest, "missing_reason", "核验不通过需填写原因")
			}
			updates["status"] = model.ClaimRejected
			updates["reject_reason"] = req.RejectReason
		}
		return tx.Model(&claim).Updates(updates).Error
	})
	if err != nil {
		respondErr(c, err)
		return
	}
	var claim model.Claim
	s.db.Preload("Authorization").First(&claim, c.Param("id"))
	c.JSON(200, maskedClaim(&claim))
}

// createAuthorization 代领需登记有效授权（授权人=失主，代领人证件）
func (s *Server) createAuthorization(c *gin.Context) {
	var req struct {
		GrantorName string `json:"grantor_name" binding:"required"`
		ProxyName   string `json:"proxy_name" binding:"required"`
		ProxyIDNo   string `json:"proxy_id_no" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondErr(c, errBiz(http.StatusBadRequest, "missing_field", "授权人、代领人姓名及证件号为必填"))
		return
	}

	err := s.db.Transaction(func(tx *gorm.DB) error {
		var claim model.Claim
		if err := tx.First(&claim, c.Param("id")).Error; err != nil {
			return errClaimNotFound
		}
		if claim.PickupType != model.PickupProxy {
			return errBiz(http.StatusBadRequest, "not_proxy", "该认领单不是代领方式")
		}
		if claim.Status != model.ClaimVerified {
			return errBiz(http.StatusConflict, "not_verified", "认领人身份未核验，不能登记代领授权")
		}
		var cnt int64
		tx.Model(&model.Authorization{}).Where("claim_id = ? AND status = ?", claim.ID, model.AuthActive).Count(&cnt)
		if cnt > 0 {
			return errBiz(http.StatusConflict, "auth_exists", "该认领单已有有效授权")
		}
		auth := model.Authorization{
			ClaimID:     claim.ID,
			ItemID:      claim.ItemID,
			GrantorName: req.GrantorName,
			ProxyName:   req.ProxyName,
			ProxyIDNo:   req.ProxyIDNo,
			Status:      model.AuthActive,
		}
		return tx.Create(&auth).Error
	})
	if err != nil {
		respondErr(c, err)
		return
	}
	var claim model.Claim
	s.db.Preload("Authorization").First(&claim, c.Param("id"))
	c.JSON(http.StatusCreated, maskedClaim(&claim))
}

// revokeAuthorization 撤销授权：撤销后代领交付会被拦截
func (s *Server) revokeAuthorization(c *gin.Context) {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var auth model.Authorization
		if err := tx.Where("claim_id = ?", c.Param("id")).First(&auth).Error; err != nil {
			return errBiz(http.StatusNotFound, "auth_not_found", "该认领单没有授权记录")
		}
		if auth.Status != model.AuthActive {
			return errBiz(http.StatusConflict, "already_revoked", "授权已撤销")
		}
		now := time.Now()
		return tx.Model(&auth).Updates(map[string]any{"status": model.AuthRevoked, "revoked_at": &now}).Error
	})
	if err != nil {
		respondErr(c, err)
		return
	}
	var claim model.Claim
	s.db.Preload("Authorization").First(&claim, c.Param("id"))
	c.JSON(200, maskedClaim(&claim))
}
