package server

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"lostfound/internal/model"
)

// 暂停交付类错误：物品状态与台账需落库（事务照常提交），仅向调用方返回错误
var (
	errSealMismatch     = errBiz(http.StatusConflict, "seal_mismatch", "封签不符，已暂停交付，待经理复核")
	errRetentionExpired = errBiz(http.StatusConflict, "retention_expired", "已超过保管期，已暂停交付，待经理复核")
)

func isHoldError(err error) bool {
	return errors.Is(err, errSealMismatch) || errors.Is(err, errRetentionExpired)
}

// deliverTx 交付事务：自取/代领/寄送共用的校验与状态流转。
// 返回交付后的认领单与交接对方名称。所有校验在事务内完成，
// 状态用条件更新（WHERE status='stored'）保证两名员工同时出库只有一人成功。
func (s *Server) deliverTx(tx *gorm.DB, itemID uint, claimID uint, staffID uint, sealIntact bool, outStatus string, action string, note string) (*model.Claim, string, error) {
	var item model.Item
	if err := tx.First(&item, itemID).Error; err != nil {
		return nil, "", errItemNotFound
	}

	var claim model.Claim
	if err := tx.First(&claim, claimID).Error; err != nil {
		return nil, "", errClaimNotFound
	}
	if claim.ItemID != item.ID {
		return nil, "", errBiz(http.StatusBadRequest, "claim_item_mismatch", "认领单与物品不匹配")
	}
	if claim.Status != model.ClaimVerified {
		return nil, "", errBiz(http.StatusConflict, "not_verified", "认领人身份未核验，不能交付")
	}

	// 代领：必须有有效授权；授权撤销后拦截交付
	if claim.PickupType == model.PickupProxy {
		var auth model.Authorization
		if err := tx.Where("claim_id = ?", claim.ID).First(&auth).Error; err != nil {
			return nil, "", errBiz(http.StatusConflict, "auth_required", "代领需先登记有效授权")
		}
		if auth.Status != model.AuthActive {
			return nil, "", errBiz(http.StatusConflict, "auth_revoked", "代领授权已撤销，禁止交付")
		}
	}

	// 封签不符：暂停交付并记入台账（由调用方提交事务）
	if !sealIntact {
		tx.Model(&item).Updates(map[string]any{"status": model.StatusHold, "hold_reason": "封签不符"})
		appendHandover(tx, item.ID, model.ActHold, staffID, "", "交付时封签不符，暂停交付")
		return nil, "", errSealMismatch
	}

	// 超过保管期：暂停交付并记入台账（由调用方提交事务）
	if time.Now().After(item.RetainUntil) {
		tx.Model(&item).Updates(map[string]any{"status": model.StatusHold, "hold_reason": "超过保管期"})
		appendHandover(tx, item.ID, model.ActHold, staffID, "", "超过保管期，暂停交付")
		return nil, "", errRetentionExpired
	}

	// 原子出库：只有在库状态才能流转，并发时只有第一个更新生效
	res := tx.Model(&model.Item{}).
		Where("id = ? AND status = ?", item.ID, model.StatusStored).
		Updates(map[string]any{"status": outStatus, "hold_reason": ""})
	if res.Error != nil {
		return nil, "", res.Error
	}
	if res.RowsAffected == 0 {
		return nil, "", errConcurrent
	}

	// 当前认领单完成，其余待处理认领单关闭（避免重复交给不同人）
	if err := tx.Model(&claim).Update("status", model.ClaimCompleted).Error; err != nil {
		return nil, "", err
	}
	if err := tx.Model(&model.Claim{}).
		Where("item_id = ? AND id <> ? AND status IN ?", item.ID, claim.ID, []string{model.ClaimPending, model.ClaimVerified}).
		Updates(map[string]any{"status": model.ClaimRejected, "reject_reason": "物品已交付其他认领人"}).Error; err != nil {
		return nil, "", err
	}

	counterparty := claim.ClaimantName
	if claim.PickupType == model.PickupProxy {
		var auth model.Authorization
		tx.Where("claim_id = ?", claim.ID).First(&auth)
		counterparty = fmt.Sprintf("%s（代领人，授权人：%s）", auth.ProxyName, auth.GrantorName)
	}
	if err := appendHandover(tx, item.ID, action, staffID, counterparty, note); err != nil {
		return nil, "", err
	}
	return &claim, counterparty, nil
}

// checkout 前台办理自取/代领出库
func (s *Server) checkout(c *gin.Context) {
	staff := currentStaff(c)
	var req struct {
		ClaimID    uint `json:"claim_id" binding:"required"`
		SealIntact bool `json:"seal_intact"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondErr(c, errBiz(http.StatusBadRequest, "missing_field", "缺少认领单号"))
		return
	}

	var counterparty string
	var holdErr error
	err := s.db.Transaction(func(tx *gorm.DB) error {
		claim, cp, err := s.deliverTx(tx, mustUint(c.Param("id")), req.ClaimID, staff.ID, req.SealIntact, model.StatusDelivered, model.ActCheckout, "")
		if err != nil {
			if isHoldError(err) {
				holdErr = err
				return nil // 暂停交付的状态与台账需要落库，事务照常提交
			}
			return err
		}
		if claim.PickupType == model.PickupShip {
			return errBiz(http.StatusBadRequest, "use_ship", "寄送认领单请使用寄送接口")
		}
		counterparty = cp
		return nil
	})
	if err != nil {
		respondErr(c, err)
		return
	}
	if holdErr != nil {
		respondErr(c, holdErr)
		return
	}
	c.JSON(200, gin.H{"ok": true, "delivered_to": counterparty})
}

// ship 办理快递寄送，成功后调用模拟寄送通知
func (s *Server) ship(c *gin.Context) {
	staff := currentStaff(c)
	var req struct {
		ClaimID    uint   `json:"claim_id" binding:"required"`
		SealIntact bool   `json:"seal_intact"`
		Address    string `json:"address" binding:"required"`
		TrackingNo string `json:"tracking_no" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondErr(c, errBiz(http.StatusBadRequest, "missing_field", "缺少认领单号、收件地址或快递单号"))
		return
	}

	var claim *model.Claim
	var item model.Item
	var holdErr error
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&item, mustUint(c.Param("id"))).Error; err != nil {
			return errItemNotFound
		}
		cl, _, err := s.deliverTx(tx, item.ID, req.ClaimID, staff.ID, req.SealIntact, model.StatusShipped, model.ActShip,
			"快递单号："+req.TrackingNo+"，寄送至："+req.Address)
		if err != nil {
			if isHoldError(err) {
				holdErr = err
				return nil // 暂停交付的状态与台账需要落库，事务照常提交
			}
			return err
		}
		claim = cl
		return nil
	})
	if err != nil {
		respondErr(c, err)
		return
	}
	if holdErr != nil {
		respondErr(c, holdErr)
		return
	}

	// 模拟寄送通知（接口失败不影响已完成的出库）
	if s.notifier != nil {
		_ = s.notifier.SendShippingNotice(claim.ClaimantName, claim.Phone, item.BagNo, req.TrackingNo, req.Address)
	}
	c.JSON(200, gin.H{"ok": true, "tracking_no": req.TrackingNo, "notified": true})
}

func mustUint(s string) uint {
	var id uint
	fmt.Sscanf(s, "%d", &id)
	return id
}
