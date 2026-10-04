# 酒店遗留物保管与失主认领系统

保洁登记客房发现的遗留物品，前台核对认领人身份后办理自取、代领或寄送，
酒店可追查物品从发现到出库的每一次交接。

## 快速开始

```bash
./run.sh          # 一条命令：构建前端 + 启动后端，访问 http://localhost:8080
go test ./...     # 运行验证测试（需先 export PATH=$HOME/sdk/go/bin:$PATH）
```

- 后端：Go + Gin + GORM，SQLite 存台账与认领记录（`data/lostfound.db`）
- 前端：Vue 3 + Vite，构建产物由后端直接托管
- 照片：保存在本地 `data/photos/`，通过 `/photos/` 访问
- 寄送通知：模拟接口，写 `data/notify.log` 并打印控制台

## 业务规则

| 规则 | 实现 |
|---|---|
| 登记 | 房号、发现时间、照片、封袋号（唯一），自动写入交接台账 |
| 敏感信息 | 证件/现金在普通搜索只显示必要信息；完整详情仅前台/经理可见 |
| 物品时间线 | 按时间展示保管位置、经手员工、状态变化（交接记录只追加，数据库触发器禁止改删） |
| 多人认领 | 一件物品可登记多个认领人，分别核验身份 |
| 代领 | 必须登记有效授权（授权人=失主）；授权撤销后交付被拦截（`auth_revoked`） |
| 暂停交付 | 封签不符或超过保管期（默认 90 天）自动转「暂停交付」，需经理复核恢复 |
| 并发安全 | 出库用条件更新 `WHERE status='stored'`，两名员工同时出库只成功一次；成交后其余认领单自动关闭，避免重复交付 |

## 主要接口

```
POST /api/items                        登记物品（multipart，含照片）
GET  /api/items?q=&room_no=&status=    搜索（敏感物品脱敏）
GET  /api/items/:id                    详情：物品 + 时间线 + 认领记录
POST /api/items/:id/claims             登记认领人
POST /api/claims/:id/verify            核验认领人身份
POST /api/claims/:id/authorization     登记代领授权
POST /api/claims/:id/authorization/revoke  撤销授权
POST /api/items/:id/checkout           自取/代领出库
POST /api/items/:id/ship               快递寄送（触发模拟通知）
POST /api/items/:id/move               变更保管位置
POST /api/items/:id/resume             经理复核恢复交付
```

员工身份通过请求头 `X-Staff-Id` 标识（演示用简化鉴权），角色：
保洁（登记）/ 前台（核验与交付）/ 经理（复核恢复）。

## 验证测试（`internal/server/server_test.go`）

- `TestDuplicateClaim`：两名认领人分别核验，交付一人后另一人无法重复领取，且不再受理新认领
- `TestConcurrentCheckout`：两名员工并发出库同一物品，恰好成功一次，台账只有一条出库记录
- `TestProxyAuthorizationRevoked`：代领授权撤销后交付被拦截
- `TestSealMismatchAndExpired`：封签不符 / 超期 → 暂停交付，经理恢复后才可出库
- `TestSensitiveMasking`：证件/现金搜索脱敏，未核验身份不可见详情
- `TestHandoverAppendOnly`：交接记录禁止更新与删除

## 目录结构

```
main.go                  入口：建库、触发器、种子员工、启动
internal/model/          数据模型与状态常量
internal/server/         路由、物品/认领/出库 handler、测试
internal/notify/         模拟寄送通知
frontend/                Vue 3 前端（搜索 / 登记 / 详情办理）
run.sh                   一条命令启动
data/                    SQLite 库、照片、通知日志（运行时生成）
```
