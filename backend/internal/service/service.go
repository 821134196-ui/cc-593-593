package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"hotel-lostfound/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrNotFound         = errors.New("记录不存在")
	ErrAlreadyReleased  = errors.New("物品已出库，不能重复交付")
	ErrDeliveryPaused   = errors.New("交付已暂停")
	ErrSealMismatch     = errors.New("封签与封袋号不符")
	ErrExpired          = errors.New("已超过保管期")
	ErrClaimNotReady    = errors.New("认领人身份尚未核实通过")
	ErrAuthInvalid      = errors.New("代领授权无效或已撤销")
	ErrIdentityMismatch = errors.New("领取人身份与登记信息不符")
)

// ConflictError 业务规则冲突（HTTP 409）
type ConflictError struct{ Err error }

func (e *ConflictError) Error() string { return e.Err.Error() }
func (e *ConflictError) Unwrap() error { return e.Err }

type Service struct {
	db        *gorm.DB
	uploadDir string
}

func New(db *gorm.DB, uploadDir string) *Service {
	return &Service{db: db, uploadDir: uploadDir}
}

func (s *Service) UploadDir() string { return s.uploadDir }

// ---------------- 登记 ----------------

type RegisterInput struct {
	RoomNumber      string
	FoundAt         time.Time
	Description     string
	PublicSummary   string
	Category        string
	BagSealNumber   string
	StorageLocation string
	RetentionDays   int
	PhotoPath       string
}

func (s *Service) RegisterItem(in RegisterInput, emp domain.Employee) (domain.Item, error) {
	if in.FoundAt.IsZero() {
		in.FoundAt = time.Now()
	}
	if in.RetentionDays <= 0 {
		in.RetentionDays = 90
	}
	if in.PublicSummary == "" {
		in.PublicSummary = defaultPublicSummary(in.Description, in.Category)
	}
	item := domain.Item{
		ID:              uuid.NewString(),
		RoomNumber:      in.RoomNumber,
		FoundAt:         in.FoundAt,
		FoundByID:       emp.ID,
		Description:     in.Description,
		PublicSummary:   in.PublicSummary,
		Category:        in.Category,
		Sensitivity:     sensitivityOf(in.Category),
		PhotoPath:       in.PhotoPath,
		BagSealNumber:   in.BagSealNumber,
		StorageLocation: in.StorageLocation,
		Status:          domain.StatusInStorage,
		CurrentSeal:     in.BagSealNumber,
		RetentionUntil:  in.FoundAt.AddDate(0, 0, in.RetentionDays),
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&item).Error; err != nil {
			return err
		}
		return s.appendHandover(tx, item.ID, domain.HandoverFound, emp, domain.Employee{},
			item.StorageLocation, fmt.Sprintf("保洁在 %s 房发现物品，封袋号 %s，存入 %s", item.RoomNumber, item.BagSealNumber, item.StorageLocation))
	})
	return item, err
}

// ---------------- 搜索（脱敏） ----------------

type ItemView struct {
	domain.Item
	FoundByName string `json:"found_by_name"`
	// 普通搜索只返回必要信息；Description / 证件号等敏感详情不包含在列表中
	Sensitive bool   `json:"sensitive"`
	PhotoURL  string `json:"photo_url,omitempty"`
}

func (s *Service) Search(q, category string) ([]ItemView, error) {
	tx := s.db.Model(&domain.Item{}).Preload("FoundBy").Order("found_at DESC")
	if category != "" {
		tx = tx.Where("category = ?", category)
	}
	q = strings.TrimSpace(q)
	if q != "" {
		like := "%" + q + "%"
		tx = tx.Where("room_number LIKE ? OR public_summary LIKE ? OR bag_seal_number LIKE ?", like, like, like)
	}
	var items []domain.Item
	if err := tx.Find(&items).Error; err != nil {
		return nil, err
	}
	views := make([]ItemView, 0, len(items))
	for _, it := range items {
		v := ItemView{Item: it, Sensitive: it.Sensitivity != domain.SensitivityNormal}
		if it.FoundBy.FullName != "" {
			v.FoundByName = it.FoundBy.FullName
		}
		if it.PhotoPath != "" {
			v.PhotoURL = "/api/photos/" + it.PhotoPath
		}
		// 列表中的描述一律使用必要信息摘要，隐藏完整描述（证件号/金额）
		v.Item.Description = it.PublicSummary
		v.PhotoPath = it.PhotoPath
		views = append(views, v)
	}
	return views, nil
}

// ---------------- 物品详情 / 时间线 ----------------

type ItemDetail struct {
	Item           domain.Item                 `json:"item"`
	FoundByName    string                      `json:"found_by_name"`
	Claims         []domain.Claim              `json:"claims"`
	Authorizations []domain.ProxyAuthorization `json:"authorizations"`
	Handovers      []domain.HandoverRecord     `json:"handovers"`
	Deliveries     []domain.Delivery           `json:"deliveries"`
	Holds          []domain.DeliveryHold       `json:"holds"`
	PhotoURL       string                      `json:"photo_url,omitempty"`
}

func (s *Service) GetItem(id string) (ItemDetail, error) {
	var d ItemDetail
	if err := s.db.First(&d.Item, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return d, ErrNotFound
		}
		return d, err
	}
	var finder domain.Employee
	s.db.First(&finder, "id = ?", d.Item.FoundByID)
	d.FoundByName = finder.FullName

	s.db.Preload("Claimant").Order("created_at").Find(&d.Claims, "item_id = ?", id)
	s.db.Order("created_at").Find(&d.Authorizations, "item_id = ?", id)
	s.db.Order("seq").Find(&d.Handovers, "item_id = ?", id)
	s.db.Order("created_at").Find(&d.Deliveries, "item_id = ?", id)
	s.db.Order("created_at").Find(&d.Holds, "item_id = ?", id)
	if d.Item.PhotoPath != "" {
		d.PhotoURL = "/api/photos/" + d.Item.PhotoPath
	}
	return d, nil
}

// MoveLocation 变更保管位置（仅追加一条交接记录）
func (s *Service) MoveLocation(itemID, location string, emp domain.Employee) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var item domain.Item
		if err := tx.First(&item, "id = ?", itemID).Error; err != nil {
			return ErrNotFound
		}
		if item.Status != domain.StatusInStorage {
			return &ConflictError{fmt.Errorf("物品已出库，无法移位")}
		}
		if err := tx.Model(&item).Update("storage_location", location).Error; err != nil {
			return err
		}
		return s.appendHandover(tx, itemID, domain.HandoverMove, emp, domain.Employee{}, location,
			fmt.Sprintf("保管位置变更为 %s", location))
	})
}

// ---------------- 认领登记 / 核实 ----------------

func (s *Service) FileClaim(itemID string, claimant domain.Claimant, description string) (domain.Claim, error) {
	var claim domain.Claim
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var item domain.Item
		if err := tx.First(&item, "id = ?", itemID).Error; err != nil {
			return ErrNotFound
		}
		if item.Status == domain.StatusReleased {
			return &ConflictError{ErrAlreadyReleased}
		}
		claimant.ID = uuid.NewString()
		if err := tx.Create(&claimant).Error; err != nil {
			return err
		}
		claim = domain.Claim{
			ID:          uuid.NewString(),
			ItemID:      itemID,
			ClaimantID:  claimant.ID,
			Description: description,
			Status:      domain.ClaimPending,
		}
		if err := tx.Create(&claim).Error; err != nil {
			return err
		}
		return s.appendHandover(tx, itemID, domain.HandoverClaimFiled, domain.Employee{}, domain.Employee{},
			"", fmt.Sprintf("认领人 %s（%s）登记认领，待核实", claimant.FullName, maskPhone(claimant.Phone)))
	})
	return claim, err
}

// VerifyClaim 前台核实认领人身份。多个认领人分别核实、互不影响。
func (s *Service) VerifyClaim(claimID string, approved bool, note string, emp domain.Employee) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var claim domain.Claim
		if err := tx.Preload("Claimant").First(&claim, "id = ?", claimID).Error; err != nil {
			return ErrNotFound
		}
		if claim.Status == domain.ClaimDelivered {
			return &ConflictError{fmt.Errorf("该认领已交付，不能重复核实")}
		}
		if claim.Status == domain.ClaimVerified {
			return &ConflictError{fmt.Errorf("该认领人已核实通过")}
		}
		now := time.Now()
		action := domain.HandoverClaimVerify
		if approved {
			claim.Status = domain.ClaimVerified
			claim.VerifiedBy = emp.ID
			claim.VerifiedAt = &now
			note = fmt.Sprintf("前台 %s 核实 %s 身份通过。%s", emp.FullName, claim.Claimant.FullName, note)
		} else {
			claim.Status = domain.ClaimRejected
			claim.RejectNote = note
			action = domain.HandoverClaimReject
			note = fmt.Sprintf("前台 %s 核实不通过：%s", emp.FullName, note)
		}
		if err := tx.Save(&claim).Error; err != nil {
			return err
		}
		return s.appendHandover(tx, claim.ItemID, action, emp, domain.Employee{}, "", strings.TrimSpace(note))
	})
}

// ---------------- 代领授权 ----------------

func (s *Service) CreateAuthorization(ownerClaimID, proxyName, proxyIDType, proxyIDNumber string, ttl time.Duration, emp domain.Employee) (domain.ProxyAuthorization, error) {
	var auth domain.ProxyAuthorization
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var claim domain.Claim
		if err := tx.First(&claim, "id = ?", ownerClaimID).Error; err != nil {
			return ErrNotFound
		}
		if claim.Status != domain.ClaimVerified {
			return &ConflictError{fmt.Errorf("只有已核实通过的失主本人才能授权代领")}
		}
		var item domain.Item
		if err := tx.First(&item, "id = ?", claim.ItemID).Error; err != nil {
			return err
		}
		if item.Status != domain.StatusInStorage {
			return &ConflictError{ErrAlreadyReleased}
		}
		auth = domain.ProxyAuthorization{
			ID:            uuid.NewString(),
			ItemID:        claim.ItemID,
			OwnerClaimID:  ownerClaimID,
			ProxyName:     proxyName,
			ProxyIDType:   proxyIDType,
			ProxyIDNumber: proxyIDNumber,
			Status:        domain.AuthorizationActive,
			GrantedBy:     emp.ID,
			ExpiresAt:     time.Now().Add(ttl),
		}
		if err := tx.Create(&auth).Error; err != nil {
			return err
		}
		return s.appendHandover(tx, claim.ItemID, domain.HandoverAuthCreate, emp, domain.Employee{}, "",
			fmt.Sprintf("失主授权 %s（%s）代领，有效期至 %s", proxyName, proxyIDType, auth.ExpiresAt.Format("2006-01-02 15:04")))
	})
	return auth, err
}

func (s *Service) RevokeAuthorization(authID string, emp domain.Employee) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var auth domain.ProxyAuthorization
		if err := tx.First(&auth, "id = ?", authID).Error; err != nil {
			return ErrNotFound
		}
		if auth.Status != domain.AuthorizationActive {
			return &ConflictError{fmt.Errorf("授权状态为 %s，无需撤销", auth.Status)}
		}
		now := time.Now()
		auth.Status = domain.AuthorizationRevoked
		auth.RevokedBy = emp.ID
		auth.RevokedAt = &now
		if err := tx.Save(&auth).Error; err != nil {
			return err
		}
		return s.appendHandover(tx, auth.ItemID, domain.HandoverAuthRevoke, emp, domain.Employee{}, "",
			fmt.Sprintf("代领授权已撤销，代领人 %s 不能再领取", auth.ProxyName))
	})
}

// ---------------- 暂停交付 / 恢复 ----------------

// HoldDelivery 登记一条暂停交付（封签不符 / 超期等）
func (s *Service) HoldDelivery(tx *gorm.DB, itemID, holdType, reason string, emp domain.Employee) error {
	hold := domain.DeliveryHold{
		ID:        uuid.NewString(),
		ItemID:    itemID,
		Reason:    reason,
		HoldType:  holdType,
		CreatedBy: emp.ID,
		CreatedAt: time.Now(),
	}
	if err := tx.Create(&hold).Error; err != nil {
		return err
	}
	return s.appendHandover(tx, itemID, domain.HandoverDeliveryHold, emp, domain.Employee{}, "",
		fmt.Sprintf("交付暂停（%s）：%s", holdType, reason))
}

func (s *Service) ResolveHold(holdID, note string, extendDays int, emp domain.Employee) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var hold domain.DeliveryHold
		if err := tx.First(&hold, "id = ?", holdID).Error; err != nil {
			return ErrNotFound
		}
		if hold.Resolved {
			return &ConflictError{fmt.Errorf("该暂停记录已处理")}
		}
		now := time.Now()
		hold.Resolved = true
		hold.ResolvedAt = &now
		if err := tx.Save(&hold).Error; err != nil {
			return err
		}
		if extendDays > 0 {
			if err := tx.Model(&domain.Item{}).Where("id = ?", hold.ItemID).
				Updates(map[string]any{"status": domain.StatusInStorage, "retention_until": now.AddDate(0, 0, extendDays)}).Error; err != nil {
				return err
			}
		}
		return s.appendHandover(tx, hold.ItemID, domain.HandoverSealCheck, emp, domain.Employee{}, "",
			fmt.Sprintf("暂停交付已解除：%s（延长保管 %d 天）", note, extendDays))
	})
}

// ---------------- 出库交付（并发安全，整件物品只能成功一次） ----------------

type ReleaseInput struct {
	ItemID        string `json:"item_id"`
	ClaimID       string `json:"claim_id"`
	Method        string `json:"method"`         // self_pickup / proxy / shipping
	SealPresented string `json:"seal_presented"` // 出库时现场核到的封袋（封签）号
	// 代领
	AuthorizationID string `json:"authorization_id"`
	ProxyName       string `json:"proxy_name"`
	ProxyIDNumber   string `json:"proxy_id_number"`
	// 寄送
	ShippingAddr string `json:"shipping_addr"`
}

type ReleaseResult struct {
	Delivery     domain.Delivery              `json:"delivery"`
	Notification *domain.ShippingNotification `json:"notification,omitempty"`
}

func (s *Service) Release(in ReleaseInput, emp domain.Employee) (ReleaseResult, error) {
	var res ReleaseResult
	tx := s.db.Begin()
	if tx.Error != nil {
		return res, tx.Error
	}
	// reject：回滚并返回业务冲突
	reject := func(e error) (ReleaseResult, error) {
		tx.Rollback()
		return res, e
	}
	// pause：暂停类拦截需要保留暂停记录与交接事件，先提交再返回冲突
	pause := func(e error) (ReleaseResult, error) {
		if cerr := tx.Commit().Error; cerr != nil {
			return res, cerr
		}
		return res, e
	}

	// 1) 取出物品（连接池 MaxOpenConns=1，写事务在连接层面串行化）
	var item domain.Item
	if err := tx.First(&item, "id = ?", in.ItemID).Error; err != nil {
		tx.Rollback()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return res, ErrNotFound
		}
		return res, err
	}

	// 2) 一件物品只能出库一次：状态 + 唯一索引双重保险
	if item.Status == domain.StatusReleased {
		return reject(&ConflictError{ErrAlreadyReleased})
	}

	// 3) 已有未解除的暂停交付 → 拦截
	var openHolds int64
	tx.Model(&domain.DeliveryHold{}).Where("item_id = ? AND resolved = ?", item.ID, false).Count(&openHolds)
	if openHolds > 0 {
		return reject(&ConflictError{fmt.Errorf("%w：存在未解除的暂停交付记录", ErrDeliveryPaused)})
	}

	// 4) 保管期：到期物品先暂停交付（记录落库后返回，不得回滚）
	if time.Now().After(item.RetentionUntil) {
		if err := s.HoldDelivery(tx, item.ID, "expired",
			fmt.Sprintf("保管期已于 %s 截止，需酒店审批延期", item.RetentionUntil.Format("2006-01-02")), emp); err != nil {
			tx.Rollback()
			return res, err
		}
		if err := tx.Model(&item).Update("status", domain.StatusExpired).Error; err != nil {
			tx.Rollback()
			return res, err
		}
		return pause(&ConflictError{ErrExpired})
	}

	// 5) 认领记录必须核实通过
	var claim domain.Claim
	if err := tx.Preload("Claimant").First(&claim, "id = ? AND item_id = ?", in.ClaimID, item.ID).Error; err != nil {
		tx.Rollback()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return res, ErrNotFound
		}
		return res, err
	}
	if claim.Status != domain.ClaimVerified {
		return reject(&ConflictError{ErrClaimNotReady})
	}

	// 6) 封签核验：现场封签号必须与登记封袋号一致，否则暂停交付（记录落库后返回）
	if in.SealPresented != item.CurrentSeal {
		if err := s.HoldDelivery(tx, item.ID, "seal_mismatch",
			fmt.Sprintf("现场封签 %q 与登记封袋号 %q 不符", in.SealPresented, item.CurrentSeal), emp); err != nil {
			tx.Rollback()
			return res, err
		}
		return pause(&ConflictError{ErrSealMismatch})
	}

	recipient := claim.Claimant.FullName
	var authID string

	switch in.Method {
	case domain.DeliverySelfPickup:
		// 本人自取：领取人姓名须与认领人一致
		if in.ProxyName != "" && in.ProxyName != claim.Claimant.FullName {
			return reject(&ConflictError{ErrIdentityMismatch})
		}
	case domain.DeliveryProxy:
		// 代领：授权必须存在、有效、未撤销、未过期、未使用，代领人身份须匹配
		var auth domain.ProxyAuthorization
		if err := tx.First(&auth, "id = ? AND item_id = ?", in.AuthorizationID, item.ID).Error; err != nil {
			return reject(&ConflictError{ErrAuthInvalid})
		}
		if auth.Status != domain.AuthorizationActive {
			return reject(&ConflictError{fmt.Errorf("%w：授权已%s", ErrAuthInvalid, auth.Status)})
		}
		if time.Now().After(auth.ExpiresAt) {
			return reject(&ConflictError{fmt.Errorf("%w：授权已过期", ErrAuthInvalid)})
		}
		if auth.OwnerClaimID != claim.ID {
			return reject(&ConflictError{fmt.Errorf("%w：授权与当前认领人不符", ErrAuthInvalid)})
		}
		if in.ProxyName != auth.ProxyName || (auth.ProxyIDNumber != "" && in.ProxyIDNumber != auth.ProxyIDNumber) {
			return reject(&ConflictError{ErrIdentityMismatch})
		}
		now := time.Now()
		auth.Status = domain.AuthorizationUsed
		auth.UsedAt = &now
		if err := tx.Save(&auth).Error; err != nil {
			tx.Rollback()
			return res, err
		}
		authID = auth.ID
		recipient = fmt.Sprintf("%s（代领，失主 %s）", auth.ProxyName, claim.Claimant.FullName)
	case domain.DeliveryShipping:
		if strings.TrimSpace(in.ShippingAddr) == "" {
			return reject(&ConflictError{fmt.Errorf("寄送必须填写收件地址")})
		}
	default:
		return reject(&ConflictError{fmt.Errorf("未知交付方式 %q", in.Method)})
	}

	// 7) 写出库记录——deliveries.item_id 唯一索引兜底并发重复
	delivery := domain.Delivery{
		ID:              uuid.NewString(),
		ItemID:          item.ID,
		Method:          in.Method,
		ClaimID:         claim.ID,
		AuthorizationID: authID,
		RecipientName:   recipient,
		OperatorID:      emp.ID,
		OperatorName:    emp.FullName,
		SealVerified:    true,
		ShippingAddr:    in.ShippingAddr,
		CreatedAt:       time.Now(),
	}
	if err := tx.Create(&delivery).Error; err != nil {
		tx.Rollback()
		if strings.Contains(err.Error(), "UNIQUE") {
			return res, &ConflictError{ErrAlreadyReleased}
		}
		return res, err
	}

	// 8) 物品出库 + 认领记录置为已交付 + 版本号推进
	now := time.Now()
	resUp := tx.Model(&domain.Item{}).Where("id = ? AND status <> ?", item.ID, domain.StatusReleased).
		Updates(map[string]any{"status": domain.StatusReleased, "released_at": now, "updated_at": now, "version": gorm.Expr("version + 1")})
	if resUp.Error != nil {
		tx.Rollback()
		return res, resUp.Error
	}
	if resUp.RowsAffected == 0 {
		// 并发中已被别人出库
		return reject(&ConflictError{ErrAlreadyReleased})
	}
	if err := tx.Model(&claim).Updates(map[string]any{"status": domain.ClaimDelivered, "updated_at": now}).Error; err != nil {
		tx.Rollback()
		return res, err
	}

	note := ""
	switch in.Method {
	case domain.DeliverySelfPickup:
		note = fmt.Sprintf("%s 本人自取，封签核验一致，经手员工 %s", recipient, emp.FullName)
	case domain.DeliveryProxy:
		note = fmt.Sprintf("%s，封签核验一致，经手员工 %s", recipient, emp.FullName)
	case domain.DeliveryShipping:
		note = fmt.Sprintf("寄送至 %s，经手员工 %s", in.ShippingAddr, emp.FullName)
	}
	if err := s.appendHandover(tx, item.ID, domain.HandoverOutbound, emp, domain.Employee{}, "", note); err != nil {
		tx.Rollback()
		return res, err
	}

	// 9) 寄送：调用模拟通知
	if in.Method == domain.DeliveryShipping {
		tracking := mockTrackingNo()
		delivery.TrackingNo = tracking
		if err := tx.Model(&delivery).Update("tracking_no", tracking).Error; err != nil {
			tx.Rollback()
			return res, err
		}
		notif := domain.ShippingNotification{
			ID:         uuid.NewString(),
			ItemID:     item.ID,
			DeliveryID: delivery.ID,
			TrackingNo: tracking,
			Address:    in.ShippingAddr,
			Recipient:  claim.Claimant.FullName,
			Channel:    "sms(mock)",
			Status:     "sent",
			CreatedAt:  time.Now(),
		}
		if err := tx.Create(&notif).Error; err != nil {
			tx.Rollback()
			return res, err
		}
		if err := s.appendHandover(tx, item.ID, domain.HandoverShipNotify, emp, domain.Employee{}, "",
			fmt.Sprintf("已模拟发送寄送通知：单号 %s → %s", tracking, in.ShippingAddr)); err != nil {
			tx.Rollback()
			return res, err
		}
		res.Notification = &notif
	}
	res.Delivery = delivery
	if err := tx.Commit().Error; err != nil {
		return res, err
	}
	return res, nil
}

// ---------------- 交接记录（只追加） ----------------

func (s *Service) appendHandover(tx *gorm.DB, itemID, action string, from, to domain.Employee, location, note string) error {
	var maxSeq int
	row := tx.Model(&domain.HandoverRecord{}).Where("item_id = ?", itemID).
		Select("COALESCE(MAX(seq), 0)").Row()
	if err := row.Scan(&maxSeq); err != nil {
		return err
	}
	rec := domain.HandoverRecord{
		ID:        uuid.NewString(),
		ItemID:    itemID,
		Seq:       maxSeq + 1,
		Action:    action,
		Location:  location,
		Note:      note,
		CreatedAt: time.Now(),
	}
	if from.ID != "" {
		rec.FromEmployeeID = from.ID
		rec.FromName = from.FullName
	}
	if to.ID != "" {
		rec.ToEmployeeID = to.ID
		rec.ToName = to.FullName
	}
	return tx.Create(&rec).Error
}

// InstallAppendOnlyTriggers 在 SQLite 层禁止修改/删除交接记录
func InstallAppendOnlyTriggers(db *gorm.DB) error {
	stmts := []string{
		`CREATE TRIGGER IF NOT EXISTS trg_handover_no_update BEFORE UPDATE ON handover_records
		 BEGIN SELECT RAISE(ABORT, '交接记录只追加，禁止修改'); END;`,
		`CREATE TRIGGER IF NOT EXISTS trg_handover_no_delete BEFORE DELETE ON handover_records
		 BEGIN SELECT RAISE(ABORT, '交接记录只追加，禁止删除'); END;`,
	}
	for _, st := range stmts {
		if err := db.Exec(st).Error; err != nil {
			return err
		}
	}
	return nil
}

// ---------------- 工具 ----------------

func sensitivityOf(category string) string {
	switch category {
	case "id":
		return domain.SensitivityID
	case "cash":
		return domain.SensitivityCash
	default:
		return domain.SensitivityNormal
	}
}

func defaultPublicSummary(desc, category string) string {
	switch category {
	case "id":
		return "证件类物品（详情已隐藏，请到前台核实）"
	case "cash":
		return "现金类物品（金额已隐藏，请到前台核实）"
	default:
		return desc
	}
}

func maskPhone(p string) string {
	if len(p) < 7 {
		return strings.Repeat("*", len(p))
	}
	return p[:3] + "****" + p[len(p)-4:]
}

func mockTrackingNo() string {
	return fmt.Sprintf("MOCK%d", time.Now().UnixNano()%1_000_000_000)
}
