package store

import (
	"log"
	"time"

	"hotel-lostfound/internal/domain"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Open 打开 SQLite 并执行迁移 / 种子数据。
func Open(dsn string) (*gorm.DB, error) {
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, err
	}

	// SQLite 默认每个连接独立事务；开启 WAL 后并发读不阻塞写。
	if err := db.Exec("PRAGMA journal_mode=WAL;").Error; err != nil {
		return nil, err
	}
	if err := db.Exec("PRAGMA busy_timeout = 5000;").Error; err != nil {
		return nil, err
	}
	// 只允许一个连接写，配合 BEGIN IMMEDIATE 才能可靠地串行化并发交付。
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)

	if err := db.AutoMigrate(
		&domain.Employee{},
		&domain.Item{},
		&domain.Claimant{},
		&domain.Claim{},
		&domain.ProxyAuthorization{},
		&domain.HandoverRecord{},
		&domain.Delivery{},
		&domain.DeliveryHold{},
		&domain.ShippingNotification{},
	); err != nil {
		return nil, err
	}

	if err := seed(db); err != nil {
		return nil, err
	}
	return db, nil
}

func seed(db *gorm.DB) error {
	var n int64
	db.Model(&domain.Employee{}).Count(&n)
	if n > 0 {
		return nil
	}
	mk := func(username, password, name, role string) domain.Employee {
		h, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		return domain.Employee{
			ID:           uuid.NewString(),
			Username:     username,
			PasswordHash: string(h),
			FullName:     name,
			Role:         role,
			CreatedAt:    time.Now(),
		}
	}
	staff := []domain.Employee{
		mk("clean1", "clean123", "王秀兰", domain.RoleHousekeeping),
		mk("clean2", "clean123", "李桂芳", domain.RoleHousekeeping),
		mk("desk1", "desk123", "陈晓前台", domain.RoleFrontDesk),
		mk("desk2", "desk123", "赵敏前台", domain.RoleFrontDesk),
	}
	if err := db.Create(&staff).Error; err != nil {
		return err
	}
	log.Printf("已创建种子员工账号: clean1/clean2(保洁, 密码 clean123), desk1/desk2(前台, 密码 desk123)")
	return nil
}
