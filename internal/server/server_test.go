package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"lostfound/internal/model"
	"lostfound/internal/notify"
	"lostfound/internal/server"
)

var (
	housekeeperID = "1"
	frontdeskID   = "2"
	managerID     = "3"
)

func setup(t *testing.T) (*httptest.Server, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	db, err := gorm.Open(sqlite.Open(filepath.Join(dir, "test.db")), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)

	if err := db.AutoMigrate(&model.Staff{}, &model.Item{}, &model.Photo{}, &model.Claim{}, &model.Authorization{}, &model.Handover{}); err != nil {
		t.Fatal(err)
	}
	db.Exec(`CREATE TRIGGER IF NOT EXISTS handovers_no_update BEFORE UPDATE ON handovers
		BEGIN SELECT RAISE(ABORT, 'handovers are append-only'); END`)
	db.Exec(`CREATE TRIGGER IF NOT EXISTS handovers_no_delete BEFORE DELETE ON handovers
		BEGIN SELECT RAISE(ABORT, 'handovers are append-only'); END`)

	db.Create(&model.Staff{Name: "保洁", Role: "housekeeper"})
	db.Create(&model.Staff{Name: "前台", Role: "frontdesk"})
	db.Create(&model.Staff{Name: "经理", Role: "manager"})

	srv := server.New(db, filepath.Join(dir, "photos"), notify.New(filepath.Join(dir, "notify.log")), 90)
	ts := httptest.NewServer(srv.Router(""))
	t.Cleanup(ts.Close)
	return ts, db
}

// registerItem 保洁登记物品
func registerItem(t *testing.T, ts *httptest.Server, bagNo, roomNo, category, desc string, foundAt time.Time) uint {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("bag_no", bagNo)
	w.WriteField("room_no", roomNo)
	w.WriteField("category", category)
	w.WriteField("description", desc)
	w.WriteField("storage_loc", "前台保管柜A")
	w.WriteField("found_at", foundAt.Format(time.RFC3339))
	w.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/api/items", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-Staff-Id", housekeeperID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("登记物品失败: %d", resp.StatusCode)
	}
	var out struct {
		ID uint `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	return out.ID
}

func createClaim(t *testing.T, ts *httptest.Server, itemID uint, name, idNo, pickup string) uint {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"claimant_name": name, "claimant_id_no": idNo,
		"description": "我的物品", "pickup_type": pickup,
	})
	req, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/items/%d/claims", ts.URL, itemID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Staff-Id", frontdeskID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建认领单失败: %d", resp.StatusCode)
	}
	var out struct {
		ID uint `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	return out.ID
}

func verifyClaim(t *testing.T, ts *httptest.Server, claimID uint) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"approve": true})
	req, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/claims/%d/verify", ts.URL, claimID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Staff-Id", frontdeskID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("核验认领单失败: %d", resp.StatusCode)
	}
}

// checkout 办理出库，返回 HTTP 状态码与错误码
func checkout(ts *httptest.Server, itemID, claimID uint, staffID string, sealIntact bool) (int, string) {
	body, _ := json.Marshal(map[string]any{"claim_id": claimID, "seal_intact": sealIntact})
	req, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/items/%d/checkout", ts.URL, itemID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Staff-Id", staffID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	var out struct {
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out.Error
}

func postJSON(ts *httptest.Server, path, staffID string, payload any) (int, map[string]any) {
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", ts.URL+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Staff-Id", staffID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, map[string]any{"error": err.Error()}
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// TestDuplicateClaim 重复认领：一件物品多个认领人分别核验，交付一人后其余人不能再领
func TestDuplicateClaim(t *testing.T) {
	ts, db := setup(t)
	itemID := registerItem(t, ts, "BAG-001", "808", "electronics", "黑色双肩包，内有充电器", time.Now())

	// 两名住客都称是自己的包，分别登记、分别核验
	claimA := createClaim(t, ts, itemID, "张三", "110101199001011234", "self")
	claimB := createClaim(t, ts, itemID, "李四", "310104198802024321", "self")
	verifyClaim(t, ts, claimA)
	verifyClaim(t, ts, claimB)

	// 张三先完成交付
	code, errCode := checkout(ts, itemID, claimA, frontdeskID, true)
	if code != http.StatusOK {
		t.Fatalf("首次交付应成功, got %d (%s)", code, errCode)
	}

	// 李四的认领单已被关闭，不能重复出库
	code, errCode = checkout(ts, itemID, claimB, frontdeskID, true)
	if code == http.StatusOK {
		t.Fatal("重复认领交付应被拦截")
	}
	t.Logf("重复认领被拦截: %d %s", code, errCode)

	// 已交付物品不再受理新认领
	code, resp := postJSON(ts, fmt.Sprintf("/api/items/%d/claims", itemID), frontdeskID, map[string]any{
		"claimant_name": "王五", "claimant_id_no": "440305199505056789",
		"description": "也是我的", "pickup_type": "self",
	})
	if code == http.StatusCreated {
		t.Fatal("已交付物品不应受理新认领")
	}
	t.Logf("交付后新认领被拦截: %d %v", code, resp["error"])

	// 台账完整：register + checkout，李四认领单被标记拒绝
	var handovers []model.Handover
	db.Where("item_id = ?", itemID).Order("id").Find(&handovers)
	if len(handovers) != 2 || handovers[1].Action != model.ActCheckout {
		t.Fatalf("台账应为 register+checkout, got %+v", handovers)
	}
	var closed model.Claim
	db.First(&closed, claimB)
	if closed.Status != model.ClaimRejected {
		t.Fatalf("未成交认领单应被关闭, got %s", closed.Status)
	}
}

// TestConcurrentCheckout 并发出库：两名员工同时办理同一物品，只能成功一次
func TestConcurrentCheckout(t *testing.T) {
	ts, db := setup(t)
	itemID := registerItem(t, ts, "BAG-002", "609", "general", "蓝色保温杯", time.Now())
	claimID := createClaim(t, ts, itemID, "赵六", "510107199003031111", "self")
	verifyClaim(t, ts, claimID)

	const workers = 2
	var wg sync.WaitGroup
	results := make([]int, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			code, _ := checkout(ts, itemID, claimID, frontdeskID, true)
			results[i] = code
		}(i)
	}
	wg.Wait()

	success, conflict := 0, 0
	for _, c := range results {
		if c == http.StatusOK {
			success++
		} else if c == http.StatusConflict {
			conflict++
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("并发出库应恰好成功一次: success=%d conflict=%d results=%v", success, conflict, results)
	}

	// 台账中只有一条 checkout 记录
	var cnt int64
	db.Model(&model.Handover{}).Where("item_id = ? AND action = ?", itemID, model.ActCheckout).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("出库交接记录应只有一条, got %d", cnt)
	}
	var item model.Item
	db.First(&item, itemID)
	if item.Status != model.StatusDelivered {
		t.Fatalf("物品应为已交付, got %s", item.Status)
	}
}

// TestProxyAuthorizationRevoked 代领授权撤销后，交付必须被拦截
func TestProxyAuthorizationRevoked(t *testing.T) {
	ts, _ := setup(t)
	itemID := registerItem(t, ts, "BAG-003", "1201", "valuable", "手表一块", time.Now())
	claimID := createClaim(t, ts, itemID, "孙七", "330106199104042222", "proxy")
	verifyClaim(t, ts, claimID)

	// 未登记授权前不能代领
	code, errCode := checkout(ts, itemID, claimID, frontdeskID, true)
	if code == http.StatusOK || errCode != "auth_required" {
		t.Fatalf("无授权代领应被拦截, got %d %s", code, errCode)
	}

	// 登记授权后可以代领的前置条件满足
	code, _ = postJSON(ts, fmt.Sprintf("/api/claims/%d/authorization", claimID), frontdeskID, map[string]any{
		"grantor_name": "孙七", "proxy_name": "周八", "proxy_id_no": "330106199205053333",
	})
	if code != http.StatusCreated {
		t.Fatalf("登记授权失败: %d", code)
	}

	// 撤销授权
	code, _ = postJSON(ts, fmt.Sprintf("/api/claims/%d/authorization/revoke", claimID), frontdeskID, nil)
	if code != http.StatusOK {
		t.Fatalf("撤销授权失败: %d", code)
	}

	// 撤销后代领交付被拦截
	code, errCode = checkout(ts, itemID, claimID, frontdeskID, true)
	if code == http.StatusOK || errCode != "auth_revoked" {
		t.Fatalf("授权撤销后交付应被拦截, got %d %s", code, errCode)
	}
	t.Logf("授权撤销后交付被拦截: %d %s", code, errCode)
}

// TestSealMismatchAndExpired 封签不符 / 超过保管期 → 暂停交付
func TestSealMismatchAndExpired(t *testing.T) {
	ts, db := setup(t)

	// 封签不符
	itemID := registerItem(t, ts, "BAG-004", "302", "general", "雨伞", time.Now())
	claimID := createClaim(t, ts, itemID, "吴九", "110108199306064444", "self")
	verifyClaim(t, ts, claimID)
	code, errCode := checkout(ts, itemID, claimID, frontdeskID, false)
	if code == http.StatusOK || errCode != "seal_mismatch" {
		t.Fatalf("封签不符应暂停交付, got %d %s", code, errCode)
	}
	var item model.Item
	db.First(&item, itemID)
	if item.Status != model.StatusHold {
		t.Fatalf("封签不符后物品应为暂停交付, got %s", item.Status)
	}
	// 暂停状态下不能直接交付
	code, _ = checkout(ts, itemID, claimID, frontdeskID, true)
	if code == http.StatusOK {
		t.Fatal("暂停交付状态不应允许出库")
	}
	// 经理复核恢复后可正常交付
	code, _ = postJSON(ts, fmt.Sprintf("/api/items/%d/resume", itemID), managerID, nil)
	if code != http.StatusOK {
		t.Fatalf("经理恢复失败: %d", code)
	}
	code, _ = checkout(ts, itemID, claimID, frontdeskID, true)
	if code != http.StatusOK {
		t.Fatalf("恢复后应可交付, got %d", code)
	}

	// 超过保管期（发现时间在 120 天前，保管期 90 天）
	oldID := registerItem(t, ts, "BAG-005", "215", "general", "旧外套", time.Now().AddDate(0, 0, -120))
	oldClaim := createClaim(t, ts, oldID, "郑十", "110105199407075555", "self")
	verifyClaim(t, ts, oldClaim)
	code, errCode = checkout(ts, oldID, oldClaim, frontdeskID, true)
	if code == http.StatusOK || errCode != "retention_expired" {
		t.Fatalf("超期应暂停交付, got %d %s", code, errCode)
	}
	var oldItem model.Item
	db.First(&oldItem, oldID)
	if oldItem.Status != model.StatusHold || oldItem.HoldReason != "超过保管期" {
		t.Fatalf("超期后应为暂停交付, got %s %s", oldItem.Status, oldItem.HoldReason)
	}
}

// TestSensitiveMasking 证件/现金在普通搜索中脱敏
func TestSensitiveMasking(t *testing.T) {
	ts, _ := setup(t)
	registerItem(t, ts, "BAG-006", "501", "cash", "现金人民币 5000 元，黑色钱包", time.Now())
	registerItem(t, ts, "BAG-007", "502", "document", "身份证，姓名王某某", time.Now())

	resp, _ := http.Get(ts.URL + "/api/items?q=现金")
	var list []map[string]any
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list) != 1 {
		t.Fatalf("应搜到 1 件, got %d", len(list))
	}
	if list[0]["description"] == "现金人民币 5000 元，黑色钱包" {
		t.Fatal("现金详情不应在搜索中公开")
	}
	if list[0]["sensitive"] != true {
		t.Fatal("现金应标记为敏感物品")
	}

	// 无员工身份查看详情，敏感信息仍脱敏
	resp, _ = http.Get(fmt.Sprintf("%s/api/items/%v", ts.URL, list[0]["id"]))
	var detail struct {
		Item map[string]any `json:"item"`
	}
	json.NewDecoder(resp.Body).Decode(&detail)
	resp.Body.Close()
	if detail.Item["description"] == "现金人民币 5000 元，黑色钱包" {
		t.Fatal("未核验身份不应看到现金详情")
	}

	// 前台员工可见完整信息
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/items/%v", ts.URL, list[0]["id"]), nil)
	req.Header.Set("X-Staff-Id", frontdeskID)
	resp, _ = http.DefaultClient.Do(req)
	json.NewDecoder(resp.Body).Decode(&detail)
	resp.Body.Close()
	if detail.Item["description"] != "现金人民币 5000 元，黑色钱包" {
		t.Fatal("前台应可见完整描述")
	}
}

// TestHandoverAppendOnly 交接记录只追加
func TestHandoverAppendOnly(t *testing.T) {
	_, db := setup(t)
	h := model.Handover{ItemID: 1, Action: model.ActRegister, StaffID: 1}
	if err := db.Create(&h).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&h).Update("note", "篡改").Error; err == nil {
		t.Fatal("交接记录不应允许更新")
	}
	if err := db.Delete(&h).Error; err == nil {
		t.Fatal("交接记录不应允许删除")
	}
}
