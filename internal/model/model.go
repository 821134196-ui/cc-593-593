package model

import "time"

// 物品类别
const (
	CategoryGeneral     = "general"     // 普通物品
	CategoryElectronics = "electronics" // 电子产品
	CategoryDocument    = "document"    // 证件（敏感）
	CategoryCash        = "cash"        // 现金（敏感）
	CategoryValuable    = "valuable"    // 贵重物品
)

// 物品状态
const (
	StatusStored    = "stored"    // 在库待认领
	StatusHold      = "hold"      // 暂停交付（封签不符/超过保管期）
	StatusDelivered = "delivered" // 已交付（自取/代领）
	StatusShipped   = "shipped"   // 已寄出
)

// 认领单状态
const (
	ClaimPending   = "pending"   // 待核验
	ClaimVerified  = "verified"  // 身份已核验，可交付
	ClaimRejected  = "rejected"  // 核验不通过 / 物品已交付他人
	ClaimCompleted = "completed" // 已完成交付
)

// 领取方式
const (
	PickupSelf  = "self"  // 失主自取
	PickupProxy = "proxy" // 授权代领
	PickupShip  = "ship"  // 快递寄送
)

// 授权状态
const (
	AuthActive  = "active"
	AuthRevoked = "revoked"
)

// 交接动作（追加式台账）
const (
	ActRegister = "register" // 登记入库
	ActMove     = "move"     // 变更保管位置
	ActHold     = "hold"     // 暂停交付
	ActResume   = "resume"   // 恢复可交付
	ActCheckout = "checkout" // 出库交付（自取/代领）
	ActShip     = "ship"     // 快递寄出
)

type Staff struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"not null" json:"name"`
	Role      string    `gorm:"not null" json:"role"` // housekeeper / frontdesk / manager
	CreatedAt time.Time `json:"created_at"`
}

type Item struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	BagNo       string    `gorm:"uniqueIndex;not null" json:"bag_no"` // 封袋号
	RoomNo      string    `gorm:"index;not null" json:"room_no"`
	FoundAt     time.Time `gorm:"not null" json:"found_at"`
	Category    string    `gorm:"not null" json:"category"`
	Description string    `json:"description"`
	StorageLoc  string    `json:"storage_loc"`
	Status      string    `gorm:"index;not null" json:"status"`
	HoldReason  string    `json:"hold_reason,omitempty"`
	RetainUntil time.Time `gorm:"not null" json:"retain_until"` // 保管期截止
	CreatedByID uint      `json:"created_by_id"`
	CreatedBy   Staff     `json:"created_by"`
	Photos      []Photo   `json:"photos,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// Sensitive 证件与现金在普通搜索中只暴露必要信息
func (i *Item) Sensitive() bool {
	return i.Category == CategoryDocument || i.Category == CategoryCash
}

type Photo struct {
	ID     uint   `gorm:"primaryKey" json:"id"`
	ItemID uint   `gorm:"index;not null" json:"item_id"`
	Path   string `gorm:"not null" json:"path"` // 形如 /photos/xxx.jpg
}

type Claim struct {
	ID            uint           `gorm:"primaryKey" json:"id"`
	ItemID        uint           `gorm:"index;not null" json:"item_id"`
	ClaimantName  string         `gorm:"not null" json:"claimant_name"`
	ClaimantIDNo  string         `gorm:"not null" json:"claimant_id_no"` // 证件号，接口返回时脱敏
	Phone         string         `json:"phone"`
	Description   string         `json:"description"` // 认领人提供的物品描述
	PickupType    string         `gorm:"not null" json:"pickup_type"`
	Status        string         `gorm:"index;not null" json:"status"`
	RejectReason  string         `json:"reject_reason,omitempty"`
	VerifiedByID  *uint          `json:"verified_by_id,omitempty"`
	VerifiedAt    *time.Time     `json:"verified_at,omitempty"`
	Authorization *Authorization `json:"authorization,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
}

// Authorization 代领授权：撤销后不得继续代领
type Authorization struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	ClaimID     uint       `gorm:"uniqueIndex;not null" json:"claim_id"`
	ItemID      uint       `gorm:"index;not null" json:"item_id"`
	GrantorName string     `gorm:"not null" json:"grantor_name"` // 授权人（失主）
	ProxyName   string     `gorm:"not null" json:"proxy_name"`   // 代领人
	ProxyIDNo   string     `gorm:"not null" json:"proxy_id_no"`
	Status      string     `gorm:"not null" json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

// Handover 交接记录：只追加，禁止更新/删除（数据库触发器兜底）
type Handover struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	ItemID       uint      `gorm:"index;not null" json:"item_id"`
	Action       string    `gorm:"not null" json:"action"`
	StaffID      uint      `gorm:"not null" json:"staff_id"`
	Staff        Staff     `json:"staff"`
	Counterparty string    `json:"counterparty,omitempty"` // 对方：认领人/代领人/快递单号
	Note         string    `json:"note,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}
