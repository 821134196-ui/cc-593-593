package main

import (
	"log"
	"os"
	"path/filepath"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"lostfound/internal/model"
	"lostfound/internal/notify"
	"lostfound/internal/server"
)

func main() {
	dataDir := envOr("DATA_DIR", "./data")
	port := envOr("PORT", "8080")
	staticDir := envOr("STATIC_DIR", "./frontend/dist")

	photoDir := filepath.Join(dataDir, "photos")
	for _, d := range []string{dataDir, photoDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			log.Fatalf("创建目录失败 %s: %v", d, err)
		}
	}

	db, err := gorm.Open(sqlite.Open(filepath.Join(dataDir, "lostfound.db")), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Warn),
	})
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	// SQLite 单写连接，配合条件更新保证并发安全
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}

	if err := db.AutoMigrate(&model.Staff{}, &model.Item{}, &model.Photo{}, &model.Claim{}, &model.Authorization{}, &model.Handover{}); err != nil {
		log.Fatalf("建表失败: %v", err)
	}

	// 交接记录只追加：数据库触发器兜底，禁止 UPDATE/DELETE
	db.Exec(`CREATE TRIGGER IF NOT EXISTS handovers_no_update BEFORE UPDATE ON handovers
		BEGIN SELECT RAISE(ABORT, 'handovers are append-only'); END`)
	db.Exec(`CREATE TRIGGER IF NOT EXISTS handovers_no_delete BEFORE DELETE ON handovers
		BEGIN SELECT RAISE(ABORT, 'handovers are append-only'); END`)

	seedStaff(db)

	srv := server.New(db, photoDir, notify.New(filepath.Join(dataDir, "notify.log")), 90)
	r := srv.Router(staticDir)
	log.Printf("酒店遗留物保管系统已启动: http://localhost:%s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}

func seedStaff(db *gorm.DB) {
	staff := []model.Staff{
		{Name: "王阿姨（保洁）", Role: "housekeeper"},
		{Name: "李前台", Role: "frontdesk"},
		{Name: "赵经理", Role: "manager"},
	}
	for _, st := range staff {
		var cnt int64
		db.Model(&model.Staff{}).Where("name = ?", st.Name).Count(&cnt)
		if cnt == 0 {
			db.Create(&st)
		}
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
