package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"

	"hotel-lostfound/internal/api"
	"hotel-lostfound/internal/auth"
	"hotel-lostfound/internal/service"
	"hotel-lostfound/internal/store"

	"github.com/gin-gonic/gin"
)

func main() {
	// 所有运行数据都放在 backend/data 下：SQLite 台账 + 本地照片
	baseDir := envOrDefault("DATA_DIR", filepath.Join("data"))
	dbPath := filepath.Join(baseDir, "lostfound.db")
	uploadDir := filepath.Join(baseDir, "uploads")
	webDist := filepath.Join("web", "dist")
	addr := envOrDefault("ADDR", ":8080")

	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		log.Fatalf("创建上传目录失败: %v", err)
	}

	db, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	if err := service.InstallAppendOnlyTriggers(db); err != nil {
		log.Fatalf("安装台账保护触发器失败: %v", err)
	}

	svc := service.New(db, uploadDir)
	authMgr := auth.NewManager(db)

	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	api.NewHandler(svc, authMgr, db, uploadDir).Register(r)

	// 前端静态资源（Vue 构建产物）
	if fi, err := os.Stat(webDist); err == nil && fi.IsDir() {
		r.Static("/assets", filepath.Join(webDist, "assets"))
		r.StaticFile("/favicon.ico", filepath.Join(webDist, "favicon.ico"))
		r.NoRoute(func(c *gin.Context) {
			// API 未匹配返回 404 JSON，其余路径回退到 SPA
			if len(c.Request.URL.Path) >= 4 && c.Request.URL.Path[:4] == "/api" {
				c.JSON(http.StatusNotFound, gin.H{"error": "接口不存在"})
				return
			}
			c.File(filepath.Join(webDist, "index.html"))
		})
		log.Printf("已挂载前端页面: %s", webDist)
	}

	log.Printf("酒店遗留物保管与认领系统已启动: http://localhost%s", addr)
	if err := (&http.Server{Addr: addr, Handler: r}).ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func envOrDefault(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
