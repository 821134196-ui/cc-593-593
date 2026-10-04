package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"hotel-lostfound/internal/api"
	"hotel-lostfound/internal/auth"
	"hotel-lostfound/internal/domain"
	"hotel-lostfound/internal/service"
	"hotel-lostfound/internal/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type env struct {
	db     *gorm.DB
	svc    *service.Service
	router *gin.Engine
	tokens map[string]string // username -> token
}

func setupEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := service.InstallAppendOnlyTriggers(db); err != nil {
		t.Fatalf("triggers: %v", err)
	}
	uploadDir := filepath.Join(dir, "uploads")
	svc := service.New(db, uploadDir)
	am := auth.NewManager(db)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api.NewHandler(svc, am, db, uploadDir).Register(r)

	e := &env{db: db, svc: svc, router: r, tokens: map[string]string{}}
	for _, u := range []string{"clean1", "desk1", "desk2"} {
		pw := "clean123"
		if u[:4] == "desk" {
			pw = "desk123"
		}
		tok, _, ok := am.Login(u, pw)
		if !ok {
			t.Fatalf("login %s", u)
		}
		e.tokens[u] = tok
	}
	return e
}

func empByUsername(t *testing.T, db *gorm.DB, username string) domain.Employee {
	var e domain.Employee
	if err := db.Where("username = ?", username).First(&e).Error; err != nil {
		t.Fatal(err)
	}
	return e
}

// register 直接经服务层登记一件物品
func (e *env) register(t *testing.T, cleaner string, in service.RegisterInput) domain.Item {
	if in.Category == "" {
		in.Category = "other"
	}
	if in.BagSealNumber == "" {
		in.BagSealNumber = "SEAL-" + uuid.NewString()
	}
	if in.StorageLocation == "" {
		in.StorageLocation = "一楼失物保管柜 A1"
	}
	if in.RetentionDays == 0 {
		in.RetentionDays = 90
	}
	if in.Description == "" {
		in.Description = "黑色钱包一个"
	}
	item, err := e.svc.RegisterItem(in, empByUsername(t, e.db, cleaner))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	return item
}

func (e *env) do(t *testing.T, method, path, who string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if who != "" {
		req.Header.Set("Authorization", "Bearer "+e.tokens[who])
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

func mustStatus(t *testing.T, w *httptest.ResponseRecorder, want int, ctx string) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("%s: 期望状态码 %d，实际 %d，body=%s", ctx, want, w.Code, w.Body.String())
	}
}

// fileVerifiedClaim 登记认领并由指定前台核实通过
func (e *env) fileVerifiedClaim(t *testing.T, itemID, name, desk string) domain.Claim {
	w := e.do(t, "POST", "/api/items/"+itemID+"/claims", "desk1", map[string]any{
		"full_name": name, "phone": "13800000001", "id_type": "idcard",
		"id_number": "110101199001011234", "description": "我遗失的物品",
	})
	mustStatus(t, w, http.StatusCreated, "登记认领")
	var cr struct{ Claim domain.Claim }
	_ = json.Unmarshal(w.Body.Bytes(), &cr)
	w = e.do(t, "POST", "/api/claims/"+cr.Claim.ID+"/verify", desk, map[string]any{"approved": true})
	mustStatus(t, w, http.StatusOK, "核实认领")
	var fresh domain.Claim
	e.db.First(&fresh, "id = ?", cr.Claim.ID)
	return fresh
}

// ---------- 场景一：并发出库，两名员工只能成功一次 ----------

func TestConcurrentRelease_OnlyOneSucceeds(t *testing.T) {
	e := setupEnv(t)
	item := e.register(t, "clean1", service.RegisterInput{RoomNumber: "1206", FoundAt: time.Now()})

	// 同一件物品，两名不同的认领人，分别由两位前台核实通过
	claimA := e.fileVerifiedClaim(t, item.ID, "张三", "desk1")
	claimB := e.fileVerifiedClaim(t, item.ID, "李四", "desk2")

	release := func(claimID, who string) *httptest.ResponseRecorder {
		return e.do(t, "POST", "/api/items/"+item.ID+"/release", who, map[string]any{
			"claim_id": claimID, "method": "self_pickup", "seal_presented": item.BagSealNumber,
		})
	}

	// 两名员工几乎同时点击出库
	var wg sync.WaitGroup
	results := make([]*httptest.ResponseRecorder, 2)
	start := make(chan struct{})
	wg.Add(2)
	go func() { defer wg.Done(); <-start; results[0] = release(claimA.ID, "desk1") }()
	go func() { defer wg.Done(); <-start; results[1] = release(claimB.ID, "desk2") }()
	close(start)
	wg.Wait()

	codes := []int{results[0].Code, results[1].Code}
	t.Logf("并发出库结果状态码: %v", codes)
	if (codes[0] == http.StatusOK) == (codes[1] == http.StatusOK) {
		t.Fatalf("必须恰好一个成功，实际: %v, body1=%s body2=%s",
			codes, results[0].Body.String(), results[1].Body.String())
	}
	var loser *httptest.ResponseRecorder
	if codes[0] != http.StatusOK {
		loser = results[0]
	} else {
		loser = results[1]
	}
	if loser.Code != http.StatusConflict {
		t.Fatalf("失败方应为 409，实际 %d: %s", loser.Code, loser.Body.String())
	}

	// 台账中只能有一条出库记录
	var n int64
	e.db.Model(&domain.Delivery{}).Where("item_id = ?", item.ID).Count(&n)
	if n != 1 {
		t.Fatalf("deliveries 应只有 1 条，实际 %d", n)
	}
	var outbound int64
	e.db.Model(&domain.HandoverRecord{}).Where("item_id = ? AND action = ?", item.ID, domain.HandoverOutbound).Count(&outbound)
	if outbound != 1 {
		t.Fatalf("出库交接事件应只有 1 条，实际 %d", outbound)
	}

	// 成功之后任何重复交付都必须被拒绝（哪怕是另一位前台再点一次）
	w := release(claimA.ID, "desk2")
	mustStatus(t, w, http.StatusConflict, "重复出库")

	// 物品已出库；未被交付的那一方认领记录不应被标记 delivered（赢家是非确定的）
	var d domain.Delivery
	e.db.First(&d, "item_id = ?", item.ID)
	loserClaimID := claimB.ID
	if d.ClaimID == claimB.ID {
		loserClaimID = claimA.ID
	}
	var loserClaim domain.Claim
	e.db.First(&loserClaim, "id = ?", loserClaimID)
	if loserClaim.Status == domain.ClaimDelivered {
		t.Fatalf("未交付的认领人不能被标记 delivered")
	}
}

// ---------- 场景二：重复认领 / 重复核实 ----------

func TestMultipleClaimantsVerifiedSeparately(t *testing.T) {
	e := setupEnv(t)
	item := e.register(t, "clean1", service.RegisterInput{RoomNumber: "808", FoundAt: time.Now()})

	c1 := e.fileVerifiedClaim(t, item.ID, "王五", "desk1")
	w := e.do(t, "POST", "/api/items/"+item.ID+"/claims", "desk1", map[string]any{
		"full_name": "赵六", "description": "另一个认领人",
	})
	mustStatus(t, w, http.StatusCreated, "第二个认领人")
	var cr struct{ Claim domain.Claim }
	_ = json.Unmarshal(w.Body.Bytes(), &cr)
	c2 := cr.Claim

	// 第二个认领人先核实不通过，不影响第一个已通过的认领人
	w = e.do(t, "POST", "/api/claims/"+c2.ID+"/verify", "desk2", map[string]any{"approved": false, "note": "描述不符"})
	mustStatus(t, w, http.StatusOK, "拒绝第二人")
	var c1fresh domain.Claim
	e.db.First(&c1fresh, "id = ?", c1.ID)
	if c1fresh.Status != domain.ClaimVerified {
		t.Fatalf("第一人的核实状态不应受影响，实际 %s", c1fresh.Status)
	}

	// 已核实通过的认领不能重复核实
	w = e.do(t, "POST", "/api/claims/"+c1.ID+"/verify", "desk1", map[string]any{"approved": true})
	mustStatus(t, w, http.StatusConflict, "重复核实")

	// 未核实通过的认领不能交付
	w = e.do(t, "POST", "/api/items/"+item.ID+"/release", "desk1", map[string]any{
		"claim_id": c2.ID, "method": "self_pickup", "seal_presented": item.BagSealNumber,
	})
	mustStatus(t, w, http.StatusConflict, "拒绝向未核实者交付")
}

// ---------- 场景三：代领授权撤销后必须拦截交付 ----------

func TestProxyAuthorizationRevoked(t *testing.T) {
	e := setupEnv(t)

	// 先验证：有效授权 + 代领人身份匹配 → 可以代领
	itemOK := e.register(t, "clean1", service.RegisterInput{RoomNumber: "301", FoundAt: time.Now(), BagSealNumber: "SEAL-OK-1"})
	owner := e.fileVerifiedClaim(t, itemOK.ID, "失主钱七", "desk1")
	w := e.do(t, "POST", "/api/claims/"+owner.ID+"/authorization", "desk1", map[string]any{
		"proxy_name": "代领孙八", "proxy_id_type": "idcard", "proxy_id_number": "320101199203034567", "ttl_hours": 24,
	})
	mustStatus(t, w, http.StatusCreated, "建立授权")
	var ar struct{ Authorization domain.ProxyAuthorization }
	_ = json.Unmarshal(w.Body.Bytes(), &ar)

	w = e.do(t, "POST", "/api/items/"+itemOK.ID+"/release", "desk1", map[string]any{
		"claim_id": owner.ID, "method": "proxy",
		"authorization_id": ar.Authorization.ID,
		"proxy_name":       "代领孙八", "proxy_id_number": "320101199203034567",
		"seal_presented": "SEAL-OK-1",
	})
	mustStatus(t, w, http.StatusOK, "有效授权代领成功")

	// 撤销场景：授权建立后被撤销，代领必须被拦截
	itemRev := e.register(t, "clean1", service.RegisterInput{RoomNumber: "302", FoundAt: time.Now(), BagSealNumber: "SEAL-REV-1"})
	owner2 := e.fileVerifiedClaim(t, itemRev.ID, "失主周九", "desk1")
	w = e.do(t, "POST", "/api/claims/"+owner2.ID+"/authorization", "desk1", map[string]any{
		"proxy_name": "代领吴十", "proxy_id_type": "idcard", "ttl_hours": 48,
	})
	mustStatus(t, w, http.StatusCreated, "建立第二份授权")
	_ = json.Unmarshal(w.Body.Bytes(), &ar)

	w = e.do(t, "POST", "/api/authorizations/"+ar.Authorization.ID+"/revoke", "desk2", map[string]any{})
	mustStatus(t, w, http.StatusOK, "撤销授权")

	// 撤销后代领 —— 即使封签正确、认领人已核实，也必须 409
	w = e.do(t, "POST", "/api/items/"+itemRev.ID+"/release", "desk1", map[string]any{
		"claim_id": owner2.ID, "method": "proxy",
		"authorization_id": ar.Authorization.ID,
		"proxy_name":       "代领吴十",
		"seal_presented":   "SEAL-REV-1",
	})
	mustStatus(t, w, http.StatusConflict, "授权撤销后必须拦截代领")
	t.Logf("拦截原因: %s", w.Body.String())

	// 物品仍在库，没有出库记录
	var n int64
	e.db.Model(&domain.Delivery{}).Where("item_id = ?", itemRev.ID).Count(&n)
	if n != 0 {
		t.Fatalf("被拦截的代领不应产生出库记录，实际 %d 条", n)
	}

	// 代领人身份与授权不一致也必须拦截（另一件）
	itemID2 := e.register(t, "clean2", service.RegisterInput{RoomNumber: "303", FoundAt: time.Now(), BagSealNumber: "SEAL-ID-1"})
	owner3 := e.fileVerifiedClaim(t, itemID2.ID, "失主冯某", "desk1")
	w = e.do(t, "POST", "/api/claims/"+owner3.ID+"/authorization", "desk1", map[string]any{
		"proxy_name": "陈某", "ttl_hours": 24,
	})
	mustStatus(t, w, http.StatusCreated, "建立第三份授权")
	_ = json.Unmarshal(w.Body.Bytes(), &ar)
	w = e.do(t, "POST", "/api/items/"+itemID2.ID+"/release", "desk1", map[string]any{
		"claim_id": owner3.ID, "method": "proxy",
		"authorization_id": ar.Authorization.ID,
		"proxy_name":       "冒名者",
		"seal_presented":   "SEAL-ID-1",
	})
	mustStatus(t, w, http.StatusConflict, "代领人身份不符必须拦截")
}

// ---------- 封签不符 → 暂停交付，解除前不能出库 ----------

func TestSealMismatchHold(t *testing.T) {
	e := setupEnv(t)
	item := e.register(t, "clean1", service.RegisterInput{RoomNumber: "501", FoundAt: time.Now(), BagSealNumber: "SEAL-9001"})
	claim := e.fileVerifiedClaim(t, item.ID, "郑某", "desk1")

	// 封签号不一致 → 409 并产生一条 hold
	w := e.do(t, "POST", "/api/items/"+item.ID+"/release", "desk1", map[string]any{
		"claim_id": claim.ID, "method": "self_pickup", "seal_presented": "SEAL-WRONG",
	})
	mustStatus(t, w, http.StatusConflict, "封签不符必须拦截")

	var holds []domain.DeliveryHold
	e.db.Find(&holds, "item_id = ?", item.ID)
	if len(holds) != 1 || holds[0].Resolved {
		t.Fatalf("应存在一条未解除的封签暂停记录，实际 %+v", holds)
	}

	// 即使这次封签填对了，因 hold 未解除仍须拦截
	w = e.do(t, "POST", "/api/items/"+item.ID+"/release", "desk2", map[string]any{
		"claim_id": claim.ID, "method": "self_pickup", "seal_presented": "SEAL-9001",
	})
	mustStatus(t, w, http.StatusConflict, "暂停未解除前不得出库")

	// 主管核查解除暂停后，封签正确即可出库
	w = e.do(t, "POST", "/api/holds/"+holds[0].ID+"/resolve", "desk1", map[string]any{"note": "封签完好，核验无误"})
	mustStatus(t, w, http.StatusOK, "解除暂停")
	w = e.do(t, "POST", "/api/items/"+item.ID+"/release", "desk1", map[string]any{
		"claim_id": claim.ID, "method": "self_pickup", "seal_presented": "SEAL-9001",
	})
	mustStatus(t, w, http.StatusOK, "解除后出库成功")
}

// ---------- 超过保管期 → 暂停交付 ----------

func TestExpiredHold(t *testing.T) {
	e := setupEnv(t)
	// 100 天前发现，保管 90 天 → 已过期
	item := e.register(t, "clean1", service.RegisterInput{
		RoomNumber: "601", FoundAt: time.Now().AddDate(0, 0, -100), RetentionDays: 90,
	})
	claim := e.fileVerifiedClaim(t, item.ID, "过期认领人", "desk1")
	w := e.do(t, "POST", "/api/items/"+item.ID+"/release", "desk1", map[string]any{
		"claim_id": claim.ID, "method": "self_pickup", "seal_presented": item.BagSealNumber,
	})
	mustStatus(t, w, http.StatusConflict, "超期必须暂停交付")

	var hold domain.DeliveryHold
	if err := e.db.Where("item_id = ?", item.ID).First(&hold).Error; err != nil {
		t.Fatalf("应产生超期暂停记录: %v", err)
	}

	// 酒店审批延长保管期后可继续交付
	w = e.do(t, "POST", "/api/holds/"+hold.ID+"/resolve", "desk1", map[string]any{"note": "酒店批准延期", "extend_days": 30})
	mustStatus(t, w, http.StatusOK, "延期解除")
	w = e.do(t, "POST", "/api/items/"+item.ID+"/release", "desk1", map[string]any{
		"claim_id": claim.ID, "method": "self_pickup", "seal_presented": item.BagSealNumber,
	})
	mustStatus(t, w, http.StatusOK, "延期后出库成功")
}

// ---------- 交接记录只追加，且按时间/序号成链 ----------

func TestHandoverAppendOnlyAndTimeline(t *testing.T) {
	e := setupEnv(t)
	item := e.register(t, "clean1", service.RegisterInput{RoomNumber: "701", FoundAt: time.Now()})

	if err := e.svc.MoveLocation(item.ID, "二楼保险柜", empByUsername(t, e.db, "desk1")); err != nil {
		t.Fatal(err)
	}
	claim := e.fileVerifiedClaim(t, item.ID, "timeline 人", "desk1")
	w := e.do(t, "POST", "/api/items/"+item.ID+"/release", "desk1", map[string]any{
		"claim_id": claim.ID, "method": "self_pickup", "seal_presented": item.BagSealNumber,
	})
	mustStatus(t, w, http.StatusOK, "出库")

	var recs []domain.HandoverRecord
	e.db.Order("seq").Find(&recs, "item_id = ?", item.ID)
	if len(recs) < 4 {
		t.Fatalf("时间线至少包含 发现/移位/认领/核实/出库，实际 %d 条", len(recs))
	}
	for i, r := range recs {
		if r.Seq != i+1 {
			t.Fatalf("seq 必须从 1 连续递增，位置 %d 实际 seq=%d", i, r.Seq)
		}
	}
	if recs[0].Action != domain.HandoverFound || recs[len(recs)-1].Action != domain.HandoverOutbound {
		t.Fatalf("时间线首尾动作错误: %s ... %s", recs[0].Action, recs[len(recs)-1].Action)
	}

	// 数据库层触发器：禁止修改、删除
	if err := e.db.Exec("UPDATE handover_records SET note = 'tampered' WHERE 1=1").Error; err == nil {
		t.Fatal("修改交接记录应当被触发器拒绝")
	}
	if err := e.db.Exec("DELETE FROM handover_records WHERE 1=1").Error; err == nil {
		t.Fatal("删除交接记录应当被触发器拒绝")
	}
}

// ---------- 证件/现金搜索脱敏 ----------

func TestSensitiveSearchMasking(t *testing.T) {
	e := setupEnv(t)
	e.register(t, "clean1", service.RegisterInput{
		RoomNumber: "901", FoundAt: time.Now(), Category: "id",
		Description:   "身份证，姓名某甲，号 110101199001011111",
		PublicSummary: "证件类物品（详情已隐藏，请到前台核实）",
	})
	e.register(t, "clean1", service.RegisterInput{
		RoomNumber: "902", FoundAt: time.Now(), Category: "cash",
		Description:   "现金人民币 8600 元",
		PublicSummary: "现金类物品（金额已隐藏，请到前台核实）",
	})
	e.register(t, "clean2", service.RegisterInput{
		RoomNumber: "903", FoundAt: time.Now(), Category: "electronics",
		Description: "黑色笔记本电脑",
	})

	w := e.do(t, "GET", "/api/items?q=", "desk1", nil)
	mustStatus(t, w, http.StatusOK, "搜索")
	var resp struct {
		Items []service.ItemView `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Items) != 3 {
		t.Fatalf("应返回 3 件物品，实际 %d", len(resp.Items))
	}
	for _, it := range resp.Items {
		switch it.Category {
		case "id":
			if !it.Sensitive || it.Description == "身份证，姓名某甲，号 110101199001011111" {
				t.Fatalf("证件详情在列表中泄露: %+v", it)
			}
		case "cash":
			if !it.Sensitive || it.Description == "现金人民币 8600 元" {
				t.Fatalf("现金金额在列表中泄露: %+v", it)
			}
		}
	}

	// 按住客描述搜索摘要关键词可命中
	w = e.do(t, "GET", "/api/items?q="+"证件", "desk1", nil)
	mustStatus(t, w, http.StatusOK, "按摘要搜索")
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Items) != 1 || resp.Items[0].Category != "id" {
		t.Fatalf("按「证件」摘要搜索应命中 1 件证件物品，实际 %d", len(resp.Items))
	}
}

// ---------- 寄送流程 + 模拟通知 ----------

func TestShippingAndNotification(t *testing.T) {
	e := setupEnv(t)
	item := e.register(t, "clean1", service.RegisterInput{RoomNumber: "1001", FoundAt: time.Now()})
	claim := e.fileVerifiedClaim(t, item.ID, "寄送人", "desk1")
	w := e.do(t, "POST", "/api/items/"+item.ID+"/release", "desk1", map[string]any{
		"claim_id": claim.ID, "method": "shipping",
		"seal_presented": item.BagSealNumber, "shipping_addr": "北京市海淀区中关村 1 号",
	})
	mustStatus(t, w, http.StatusOK, "寄送出库")
	var res service.ReleaseResult
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res.Notification == nil || res.Notification.TrackingNo == "" || res.Notification.Status != "sent" {
		t.Fatalf("应生成已发送的模拟通知，实际 %+v", res.Notification)
	}

	w = e.do(t, "GET", "/api/items/"+item.ID+"/notifications", "desk1", nil)
	mustStatus(t, w, http.StatusOK, "查询通知")
	if !bytes.Contains([]byte(w.Body.String()), []byte("MOCK")) {
		t.Fatalf("通知列表应包含模拟单号: %s", w.Body.String())
	}
}

func TestRoleGuard(t *testing.T) {
	e := setupEnv(t)
	// 保洁不能调出库接口（路由角色 + 业务状态双重保险之外的第一层）
	w := e.do(t, "POST", fmt.Sprintf("/api/items/%s/release", uuid.NewString()), "clean1", map[string]any{})
	if w.Code != http.StatusForbidden {
		t.Fatalf("保洁调用前台交付应 403，实际 %d", w.Code)
	}
}
