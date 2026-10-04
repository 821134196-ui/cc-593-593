package auth

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"hotel-lostfound/internal/domain"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type session struct {
	employee domain.Employee
	expires  time.Time
}

type Manager struct {
	db   *gorm.DB
	mu   sync.RWMutex
	toks map[string]session
}

func NewManager(db *gorm.DB) *Manager {
	return &Manager{db: db, toks: map[string]session{}}
}

func (m *Manager) Login(username, password string) (string, domain.Employee, bool) {
	var emp domain.Employee
	if err := m.db.Where("username = ?", username).First(&emp).Error; err != nil {
		return "", domain.Employee{}, false
	}
	if err := checkPassword(emp.PasswordHash, password); err != nil {
		return "", domain.Employee{}, false
	}
	tok := randomToken()
	m.mu.Lock()
	m.toks[tok] = session{employee: emp, expires: time.Now().Add(12 * time.Hour)}
	m.mu.Unlock()
	return tok, emp, true
}

func (m *Manager) Logout(tok string) {
	m.mu.Lock()
	delete(m.toks, tok)
	m.mu.Unlock()
}

func (m *Manager) resolve(tok string) (domain.Employee, bool) {
	m.mu.RLock()
	s, ok := m.toks[tok]
	m.mu.RUnlock()
	if !ok || time.Now().After(s.expires) {
		return domain.Employee{}, false
	}
	return s.employee, true
}

const CurrentUserKey = "currentUser"

// Middleware 校验 Bearer Token，并把员工注入上下文。
func (m *Manager) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		tok := strings.TrimPrefix(h, "Bearer ")
		if tok == "" || tok == h {
			tok = c.Query("token") // 方便 <img> 直接加载照片
		}
		emp, ok := m.resolve(tok)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "未登录或登录已过期"})
			return
		}
		c.Set(CurrentUserKey, emp)
		c.Next()
	}
}

func CurrentUser(c *gin.Context) domain.Employee {
	v, _ := c.Get(CurrentUserKey)
	return v.(domain.Employee)
}

// RequireRole 限定角色。
func RequireRole(roles ...string) gin.HandlerFunc {
	allow := map[string]bool{}
	for _, r := range roles {
		allow[r] = true
	}
	return func(c *gin.Context) {
		if !allow[CurrentUser(c).Role] {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "当前角色无权执行此操作"})
			return
		}
		c.Next()
	}
}
