package domain

import "time"

// 角色
const (
	RoleHousekeeping = "housekeeping" // 保洁
	RoleFrontDesk    = "front_desk"   // 前台
)

// 物品保管状态
const (
	StatusInStorage = "in_storage" // 待认领 / 在库
	StatusReleased  = "released"   // 已出库（自取或寄送）
	StatusExpired   = "expired"    // 超过保管期，待酒店处理
)

// 物品敏感等级：证件、现金在普通搜索中只显示必要信息
const (
	SensitivityNormal = "normal" // 普通物品
	SensitivityID     = "id"     // 证件类
	SensitivityCash   = "cash"   // 现金类
)

// 认领人验证状态
const (
	ClaimPending   = "pending"   // 已登记，待核实
	ClaimVerified  = "verified"  // 身份已核实
	ClaimRejected  = "rejected"  // 核实不通过
	ClaimDelivered = "delivered" // 已交付
)

// 授权状态
const (
	AuthorizationActive  = "active"
	AuthorizationRevoked = "revoked"
	AuthorizationUsed    = "used"
	AuthorizationExpired = "expired"
)

// 交付方式
const (
	DeliverySelfPickup = "self_pickup" // 本人自取
	DeliveryProxy      = "proxy"       // 代领
	DeliveryShipping   = "shipping"    // 寄送
)

// 交接动作（只追加的台账事件）
const (
	HandoverFound        = "found"        // 保洁发现登记
	HandoverMove         = "move"         // 保管位置变更
	HandoverSealCheck    = "seal_check"   // 封签核验
	HandoverClaimFiled   = "claim_filed"  // 认领登记
	HandoverClaimVerify  = "claim_verify" // 身份核实
	HandoverClaimReject  = "claim_reject"
	HandoverAuthCreate   = "auth_create"   // 代领授权建立
	HandoverAuthRevoke   = "auth_revoke"   // 授权撤销
	HandoverOutbound     = "outbound"      // 出库交付
	HandoverDeliveryHold = "delivery_hold" // 暂停交付（封签不符/超期）
	HandoverShipNotify   = "ship_notify"   // 寄送通知
	HandoverExpire       = "expire"        // 保管期到期
)

type Employee struct {
	ID           string    `gorm:"primaryKey;size:36" json:"id"`
	Username     string    `gorm:"uniqueIndex;size:64;not null" json:"username"`
	PasswordHash string    `gorm:"size:128;not null" json:"-"`
	FullName     string    `gorm:"size:64;not null" json:"full_name"`
	Role         string    `gorm:"size:32;not null" json:"role"`
	CreatedAt    time.Time `json:"created_at"`
}

type Item struct {
	ID              string     `gorm:"primaryKey;size:36" json:"id"`
	RoomNumber      string     `gorm:"index;size:32;not null" json:"room_number"`
	FoundAt         time.Time  `gorm:"not null;index" json:"found_at"`
	FoundByID       string     `gorm:"size:36;not null" json:"found_by_id"`
	FoundBy         Employee   `gorm:"foreignKey:FoundByID" json:"found_by,omitempty"`
	Description     string     `gorm:"not null" json:"description"`    // 完整描述（含证件号/金额等敏感信息）
	PublicSummary   string     `gorm:"not null" json:"public_summary"` // 必要信息（普通搜索可见）
	Category        string     `gorm:"size:32;not null;index" json:"category"`
	Sensitivity     string     `gorm:"size:16;not null;index" json:"sensitivity"`
	PhotoPath       string     `gorm:"size:256" json:"photo_path,omitempty"`
	BagSealNumber   string     `gorm:"uniqueIndex;size:64;not null" json:"bag_seal_number"` // 封袋号（封签号）
	StorageLocation string     `gorm:"size:128;not null" json:"storage_location"`
	Status          string     `gorm:"size:16;not null;index" json:"status"`
	CurrentSeal     string     `gorm:"size:64;not null" json:"current_seal"` // 当前封签（出库时必须与封袋号一致）
	RetentionUntil  time.Time  `gorm:"not null" json:"retention_until"`      // 保管期限
	ReleasedAt      *time.Time `json:"released_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	Version         int        `gorm:"not null;default:0" json:"version"` // 乐观锁版本
}

type Claimant struct {
	ID        string    `gorm:"primaryKey;size:36" json:"id"`
	FullName  string    `gorm:"size:64;not null" json:"full_name"`
	Phone     string    `gorm:"size:32" json:"phone"`
	IDType    string    `gorm:"size:32" json:"id_type"`
	IDNumber  string    `gorm:"size:64" json:"id_number"` // 完整证件号（仅详情页/已核实员工可见）
	CreatedAt time.Time `json:"created_at"`
}

type Claim struct {
	ID          string     `gorm:"primaryKey;size:36" json:"id"`
	ItemID      string     `gorm:"size:36;not null;index" json:"item_id"`
	ClaimantID  string     `gorm:"size:36;not null" json:"claimant_id"`
	Claimant    Claimant   `gorm:"foreignKey:ClaimantID" json:"claimant,omitempty"`
	Description string     `gorm:"not null" json:"description"` // 住客自述物品特征
	Status      string     `gorm:"size:16;not null;index" json:"status"`
	VerifiedBy  string     `gorm:"size:36" json:"verified_by,omitempty"`
	VerifiedAt  *time.Time `json:"verified_at,omitempty"`
	RejectNote  string     `json:"reject_note,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type ProxyAuthorization struct {
	ID            string     `gorm:"primaryKey;size:36" json:"id"`
	ItemID        string     `gorm:"size:36;not null;index" json:"item_id"`
	OwnerClaimID  string     `gorm:"size:36;not null;index" json:"owner_claim_id"` // 失主（已核实）的认领记录
	ProxyName     string     `gorm:"size:64;not null" json:"proxy_name"`
	ProxyIDType   string     `gorm:"size:32" json:"proxy_id_type"`
	ProxyIDNumber string     `gorm:"size:64" json:"proxy_id_number"`
	Status        string     `gorm:"size:16;not null;index" json:"status"`
	GrantedBy     string     `gorm:"size:36;not null" json:"granted_by"`
	RevokedBy     string     `gorm:"size:36" json:"revoked_by,omitempty"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
	ExpiresAt     time.Time  `gorm:"not null" json:"expires_at"`
	UsedAt        *time.Time `json:"used_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// HandoverRecord 交接台账：只追加，不修改、不删除
type HandoverRecord struct {
	ID             string    `gorm:"primaryKey;size:36" json:"id"`
	ItemID         string    `gorm:"size:36;not null;index;uniqueIndex:idx_handover_item_seq,priority:1" json:"item_id"`
	Seq            int       `gorm:"not null;uniqueIndex:idx_handover_item_seq,priority:2" json:"seq"` // 同一物品内严格递增
	Action         string    `gorm:"size:32;not null" json:"action"`
	FromEmployeeID string    `gorm:"size:36" json:"from_employee_id,omitempty"`
	ToEmployeeID   string    `gorm:"size:36" json:"to_employee_id,omitempty"`
	FromName       string    `gorm:"size:64" json:"from_name,omitempty"`
	ToName         string    `gorm:"size:64" json:"to_name,omitempty"`
	Location       string    `gorm:"size:128" json:"location,omitempty"`
	Note           string    `json:"note,omitempty"`
	CreatedAt      time.Time `gorm:"not null;index" json:"created_at"`
}

// Delivery 出库记录（一件物品至多一条成功出库）
type Delivery struct {
	ID              string    `gorm:"primaryKey;size:36" json:"id"`
	ItemID          string    `gorm:"uniqueIndex;size:36;not null" json:"item_id"` // 唯一约束：一件物品只能出库一次
	Method          string    `gorm:"size:16;not null" json:"method"`
	ClaimID         string    `gorm:"size:36;not null" json:"claim_id"`
	AuthorizationID string    `gorm:"size:36" json:"authorization_id,omitempty"`
	RecipientName   string    `gorm:"size:64;not null" json:"recipient_name"`
	OperatorID      string    `gorm:"size:36;not null" json:"operator_id"`
	OperatorName    string    `gorm:"size:64;not null" json:"operator_name"`
	SealVerified    bool      `json:"seal_verified"`
	ShippingAddr    string    `json:"shipping_addr,omitempty"`
	TrackingNo      string    `gorm:"size:64" json:"tracking_no,omitempty"`
	CreatedAt       time.Time `json:"not null" json:"created_at"`
}

// DeliveryHold 暂停交付记录
type DeliveryHold struct {
	ID         string     `gorm:"primaryKey;size:36" json:"id"`
	ItemID     string     `gorm:"size:36;not null;index" json:"item_id"`
	Reason     string     `gorm:"size:256;not null" json:"reason"`
	HoldType   string     `gorm:"size:32;not null" json:"hold_type"` // seal_mismatch / expired
	Resolved   bool       `gorm:"not null;default:false" json:"resolved"`
	CreatedBy  string     `gorm:"size:36;not null" json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

// ShippingNotification 模拟的寄送通知
type ShippingNotification struct {
	ID         string    `gorm:"primaryKey;size:36" json:"id"`
	ItemID     string    `gorm:"size:36;not null;index" json:"item_id"`
	DeliveryID string    `gorm:"size:36;not null" json:"delivery_id"`
	TrackingNo string    `gorm:"size:64;not null" json:"tracking_no"`
	Address    string    `gorm:"not null" json:"address"`
	Recipient  string    `gorm:"size:64;not null" json:"recipient"`
	Channel    string    `gorm:"size:32;not null" json:"channel"` // sms / email / mock
	Status     string    `gorm:"size:16;not null" json:"status"`  // sent（模拟发送成功）
	CreatedAt  time.Time `json:"created_at"`
}
