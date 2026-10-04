#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
端到端验证：对真实运行的服务发起 HTTP 请求，验证
  1) 重复认领：多人分别核实，一件物品只能出库一次
  2) 并发出库：两名前台同时确认交付，恰好一次成功
  3) 代领授权撤销后交付被拦截（含封签不符 / 超保管期暂停）
  4) 证件/现金普通搜索脱敏

用法：python3 scripts/verify_e2e.py
脚本会自行编译并在临时数据目录启动服务，结束后清理。
"""
import concurrent.futures
import json
import os
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import urllib.request
import urllib.error
import urllib.parse
import uuid

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
PORT = int(os.environ.get("VERIFY_PORT", "8099"))
BASE = f"http://127.0.0.1:{PORT}"

PASS, FAIL = 0, 0
FAILURES = []


def check(name, cond, detail=""):
    global PASS, FAIL
    if cond:
        PASS += 1
        print(f"  ✅ {name}")
    else:
        FAIL += 1
        FAILURES.append(f"{name} {detail}")
        print(f"  ❌ {name} {detail}")


def req(method, path, token=None, body=None, form=None):
    url = BASE + path
    headers = {}
    data = None
    if form is not None:
        data = urllib.parse.urlencode(form).encode()
        headers["Content-Type"] = "application/x-www-form-urlencoded"
    elif body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    if token:
        headers["Authorization"] = "Bearer " + token
    r = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(r) as resp:
            return resp.status, json.loads(resp.read().decode() or "{}")
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read().decode() or "{}")
        except Exception:
            return e.code, {}


def login(username, password):
    code, data = req("POST", "/api/auth/login", body={"username": username, "password": password})
    assert code == 200, f"登录失败 {username}: {data}"
    return data["token"]


def register(token, room, category="other", desc="物品", summary="", seal=None, days=90, found_at=""):
    seal = seal or f"SEAL-{uuid.uuid4().hex[:10]}"
    form = {"room_number": room, "category": category, "description": desc,
            "public_summary": summary, "bag_seal_number": seal,
            "storage_location": "一楼失物保管柜 A1", "retention_days": str(days)}
    if found_at:
        form["found_at"] = found_at
    code, data = req("POST", "/api/items", token=token, form=form)
    assert code == 201, f"登记失败: {code} {data}"
    return data["item"]


def file_claim(token, item_id, name, desc="失物特征"):
    code, data = req("POST", f"/api/items/{item_id}/claims", token=token,
                     body={"full_name": name, "phone": "13800000000",
                           "id_type": "idcard", "id_number": "11010119900101000X",
                           "description": desc})
    assert code == 201, f"认领登记失败: {data}"
    return data["claim"]["id"]


def verify(token, claim_id, approved=True, note="描述吻合"):
    return req("POST", f"/api/claims/{claim_id}/verify", token=token,
               body={"approved": approved, "note": note})


def release(token, item_id, **kw):
    return req("POST", f"/api/items/{item_id}/release", token=token, body=kw)


def get_item(token, item_id):
    code, data = req("GET", f"/api/items/{item_id}", token=token)
    assert code == 200, data
    return data


def main():
    go_bin = shutil.which("go") or os.path.join(os.environ.get("HOME", ""), "sdk", "go", "bin", "go")
    env = os.environ.copy()
    env["GOPROXY"] = "https://goproxy.cn,direct"
    print("==> 编译后端…")
    subprocess.run([go_bin, "build", "-o", "/tmp/lostfound-verify", "./cmd/server"],
                   cwd=os.path.join(ROOT, "backend"), env=env, check=True, capture_output=True)

    data_dir = tempfile.mkdtemp(prefix="lf-verify-")
    env["DATA_DIR"] = data_dir
    env["ADDR"] = f":{PORT}"
    print(f"==> 启动服务（临时数据目录 {data_dir}）…")
    proc = subprocess.Popen(["/tmp/lostfound-verify"], cwd=os.path.join(ROOT, "backend"),
                            env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    try:
        for _ in range(50):
            try:
                probe = urllib.request.Request(
                    BASE + "/api/auth/login", data=b"{}",
                    headers={"Content-Type": "application/json"}, method="POST")
                urllib.request.urlopen(probe)
                break  # 不会发生（空 body 返回 400），但能连上即可
            except urllib.error.HTTPError:
                break  # 服务已起来（400/401 都算）
            except Exception:
                time.sleep(0.1)
        else:
            raise RuntimeError("服务启动超时")

        clean = login("clean1", "clean123")
        desk1 = login("desk1", "desk123")
        desk2 = login("desk2", "desk123")

        # ---------- 场景 1：重复认领 ----------
        print("\n[场景1] 一件物品多位认领人，分别核实后只能出库一次")
        item = register(clean, "1206", "electronics", "黑色双肩包", seal="SEAL-CASE-1")
        c1 = file_claim(desk1, item["id"], "认领人甲", "黑色双肩包有挂饰")
        c2 = file_claim(desk1, item["id"], "认领人乙", "灰色背包")
        code, _ = verify(desk1, c1, True)
        check("认领人甲核实通过", code == 200)
        code, _ = verify(desk2, c2, False, "颜色不符")
        check("认领人乙被驳回且不影响甲", code == 200)
        code, _ = verify(desk1, c1, True)
        check("已核实的认领不能重复核实（409）", code == 409)
        code, _ = release(desk1, item["id"], claim_id=c2, method="self_pickup",
                          seal_presented="SEAL-CASE-1")
        check("向未核实通过的认领人交付被拦截（409）", code == 409)
        code, _ = release(desk1, item["id"], claim_id=c1, method="self_pickup",
                          seal_presented="SEAL-CASE-1")
        check("本人自取成功", code == 200)
        code, _ = release(desk2, item["id"], claim_id=c1, method="self_pickup",
                          seal_presented="SEAL-CASE-1")
        check("物品已出库后重复交付被拦截（409）", code == 409)
        detail = get_item(desk1, item["id"])
        check("出库记录恰好 1 条", len(detail["deliveries"]) == 1,
              f"实际 {len(detail['deliveries'])}")
        check("出库交接事件恰好 1 条",
              sum(1 for h in detail["handovers"] if h["action"] == "outbound") == 1)

        # ---------- 场景 2：并发出库 ----------
        print("\n[场景2] 两名前台同时对同一件物品出库（不同认领人）")
        item2 = register(clean, "1801", "electronics", "银色手表", seal="SEAL-CASE-2")
        a = file_claim(desk1, item2["id"], "并发甲")
        b = file_claim(desk2, item2["id"], "并发乙")
        verify(desk1, a, True)
        verify(desk2, b, True)

        def do_release(args):
            token, claim = args
            st, _ = release(token, item2["id"], claim_id=claim,
                            method="self_pickup", seal_presented="SEAL-CASE-2")
            return st

        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as ex:
            statuses = list(ex.map(do_release, [(desk1, a), (desk2, b)]))
        print(f"    并发响应状态码：{statuses}")
        check("并发出库恰好一个 200", statuses.count(200) == 1, str(statuses))
        check("并发出库恰好一个 409", statuses.count(409) == 1, str(statuses))
        detail2 = get_item(desk1, item2["id"])
        check("并发后出库记录仍只有 1 条", len(detail2["deliveries"]) == 1)
        delivered = {c["id"]: c["status"] for c in detail2["claims"]}
        check("仅赢家认领记录为 delivered，败者状态不变",
              list(delivered.values()).count("delivered") == 1, str(delivered))

        # ---------- 场景 3：代领授权撤销 ----------
        print("\n[场景3] 代领授权撤销 / 封签不符 / 超期 交付拦截")
        item3 = register(clean, "2002", "jewelry", "金项链", seal="SEAL-CASE-3")
        owner = file_claim(desk1, item3["id"], "失主丙")
        verify(desk1, owner, True)
        code, data = req("POST", f"/api/claims/{owner}/authorization", token=desk1,
                         body={"proxy_name": "代领丁", "proxy_id_type": "idcard",
                               "proxy_id_number": "32010119920303000X", "ttl_hours": 24})
        check("建立代领授权", code == 201, str(data))
        auth_id = data["authorization"]["id"]
        code, _ = req("POST", f"/api/authorizations/{auth_id}/revoke", token=desk2, body={})
        check("授权撤销成功", code == 200)
        code, data = release(desk1, item3["id"], claim_id=owner, method="proxy",
                             authorization_id=auth_id, proxy_name="代领丁",
                             seal_presented="SEAL-CASE-3")
        check("授权撤销后代领被拦截（409）", code == 409, str(data))
        check("拦截原因明确指出授权已撤销", "revoked" in data.get("error", "") or "撤销" in data.get("error", ""),
              data.get("error"))
        detail3 = get_item(desk1, item3["id"])
        check("被拦截后没有出库记录", len(detail3["deliveries"]) == 0)
        check("撤销事件已写入只追加台账",
              any(h["action"] == "auth_revoke" for h in detail3["handovers"]))

        # 有效授权 + 身份匹配 → 代领成功
        item4 = register(clean, "2003", "electronics", "平板", seal="SEAL-CASE-4")
        owner4 = file_claim(desk1, item4["id"], "失主戊")
        verify(desk1, owner4, True)
        _, ad = req("POST", f"/api/claims/{owner4}/authorization", token=desk1,
                    body={"proxy_name": "代领己", "ttl_hours": 24})
        code, _ = release(desk1, item4["id"], claim_id=owner4, method="proxy",
                          authorization_id=ad["authorization"]["id"],
                          proxy_name="代领己", seal_presented="SEAL-CASE-4")
        check("有效授权且代领人身份匹配 → 代领成功", code == 200)
        # 冒名代领
        item5 = register(clean, "2004", "other", "雨伞", seal="SEAL-CASE-5")
        owner5 = file_claim(desk1, item5["id"], "失主庚")
        verify(desk1, owner5, True)
        _, ad5 = req("POST", f"/api/claims/{owner5}/authorization", token=desk1,
                     body={"proxy_name": "授权代领辛", "ttl_hours": 24})
        code, _ = release(desk1, item5["id"], claim_id=owner5, method="proxy",
                          authorization_id=ad5["authorization"]["id"],
                          proxy_name="冒名者", seal_presented="SEAL-CASE-5")
        check("代领人身份与授权不符被拦截（409）", code == 409)

        # 封签不符 → 暂停，暂停未解除前即使封签正确也不能出库
        item6 = register(clean, "3001", "other", "外套", seal="SEAL-CASE-6")
        c6 = file_claim(desk1, item6["id"], "失主壬")
        verify(desk1, c6, True)
        code, data = release(desk1, item6["id"], claim_id=c6, method="self_pickup",
                             seal_presented="SEAL-BROKEN")
        check("封签不符触发暂停交付（409）", code == 409, str(data))
        code, _ = release(desk2, item6["id"], claim_id=c6, method="self_pickup",
                          seal_presented="SEAL-CASE-6")
        check("暂停未解除前即使封签正确仍被拦截（409）", code == 409)
        d6 = get_item(desk1, item6["id"])
        hold = next(h for h in d6["holds"] if not h["resolved"])
        code, _ = req("POST", f"/api/holds/{hold['id']}/resolve", token=desk1,
                      body={"note": "核查封签完好"})
        check("前台核查后解除暂停", code == 200)
        code, _ = release(desk1, item6["id"], claim_id=c6, method="self_pickup",
                          seal_presented="SEAL-CASE-6")
        check("解除暂停后出库成功", code == 200)

        # 超过保管期 → 暂停，延期后可交付
        item7 = register(clean, "3002", "other", "旧书", seal="SEAL-CASE-7",
                         days=1, found_at=time.strftime("%Y-%m-%dT%H:%M",
                                                        time.localtime(time.time() - 10 * 86400)))
        c7 = file_claim(desk1, item7["id"], "超期认领人")
        verify(desk1, c7, True)
        code, _ = release(desk1, item7["id"], claim_id=c7, method="self_pickup",
                          seal_presented="SEAL-CASE-7")
        check("超过保管期自动暂停交付（409）", code == 409)
        d7 = get_item(desk1, item7["id"])
        hold7 = next(h for h in d7["holds"] if not h["resolved"])
        req("POST", f"/api/holds/{hold7['id']}/resolve", token=desk1,
            body={"note": "酒店批准延期", "extend_days": 30})
        code, _ = release(desk1, item7["id"], claim_id=c7, method="self_pickup",
                          seal_presented="SEAL-CASE-7")
        check("酒店延期后出库成功", code == 200)

        # ---------- 场景 4：脱敏搜索 ----------
        print("\n[场景4] 证件/现金普通搜索只显示必要信息")
        register(clean, "4001", "id",
                 "身份证，姓名张三，号 110101199001011234",
                 "证件类物品（详情已隐藏，请到前台核实）", seal="SEAL-ID-1")
        register(clean, "4002", "cash", "现金人民币 9800 元整",
                 "现金类物品（金额已隐藏，请到前台核实）", seal="SEAL-CASH-1")
        code, data = req("GET", "/api/items?q=" + urllib.parse.quote(""), token=desk1)
        assert code == 200
        leaked = [i for i in data["items"]
                  if "110101199001011234" in json.dumps(i, ensure_ascii=False)
                  or "9800" in json.dumps(i, ensure_ascii=False)]
        check("搜索结果中不包含完整证件号与金额", not leaked, json.dumps(leaked, ensure_ascii=False))
        id_items = [i for i in data["items"] if i["category"] == "id"]
        check("证件物品标记为敏感且描述为必要信息",
              bool(id_items) and id_items[0]["sensitive"] is True and "详情已隐藏" in id_items[0]["description"])

        print(f"\n================ 结果：{PASS} 通过 / {FAIL} 失败 ================")
        if FAILURES:
            for f in FAILURES:
                print(" -", f)
            return 1
        return 0
    finally:
        proc.send_signal(signal.SIGTERM)
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill()
        shutil.rmtree(data_dir, ignore_errors=True)
        if os.path.exists("/tmp/lostfound-verify"):
            os.remove("/tmp/lostfound-verify")


if __name__ == "__main__":
    sys.exit(main())
