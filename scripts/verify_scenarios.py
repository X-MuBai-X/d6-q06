#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""五组完整业务场景的验证脚本（题目 06）。

用法：
    python scripts/verify_scenarios.py --base http://127.0.0.1:8080 --mock http://127.0.0.1:8090

脚本只依赖标准库，逐条断言场景结论并把过程打印到标准输出；
退出码非 0 表示存在未通过的场景断言。
"""
from __future__ import annotations

import argparse
import json
import sys
import time
import urllib.error
import urllib.request

PASSWORD = "Studio@2026"


class Client:
    """极简 JSON HTTP 客户端。"""

    def __init__(self, base: str) -> None:
        self.base = base.rstrip("/")
        self.token = ""

    def request(self, method: str, path: str, payload=None, expect=None, headers=None):
        url = self.base + path
        data = None
        hdrs = dict(headers or {})
        if payload is not None:
            data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
            hdrs["Content-Type"] = "application/json"
        if self.token:
            hdrs["Authorization"] = "Bearer " + self.token
        req = urllib.request.Request(url, data=data, headers=hdrs, method=method)
        try:
            with urllib.request.urlopen(req, timeout=15) as resp:
                body = resp.read().decode("utf-8")
                status = resp.status
        except urllib.error.HTTPError as exc:
            body = exc.read().decode("utf-8")
            status = exc.code
        parsed = json.loads(body) if body else {}
        if expect is not None and status != expect:
            raise AssertionError(f"{method} {path} 期望状态码 {expect}，实际 {status}：{body[:300]}")
        return status, parsed

    def login(self, account: str) -> "Client":
        status, body = self.request("POST", "/api/login", {"login": account, "password": PASSWORD}, expect=200)
        self.token = body["token"]
        return self


class Checks:
    """记录断言结果。"""

    def __init__(self) -> None:
        self.passed = 0
        self.failed: list[str] = []

    def check(self, name: str, condition: bool, detail: str = "") -> None:
        if condition:
            self.passed += 1
            print(f"[PASS] {name} {detail}")
        else:
            self.failed.append(name)
            print(f"[FAIL] {name} {detail}")


def wait_for(predicate, timeout=15.0, interval=0.2):
    """轮询等待条件成立，返回是否成立。"""
    deadline = time.time() + timeout
    while time.time() < deadline:
        if predicate():
            return True
        time.sleep(interval)
    return False


def set_mode(mock: Client, mode: str, delay_ms: int = 150, duplicate: bool = False) -> None:
    mock.request("POST", "/control/mode", {"mode": mode, "delay_ms": delay_ms, "duplicate": duplicate}, expect=200)


def requirement_status(client: Client, rid: str) -> str:
    _, body = client.request("GET", f"/api/requirements/{rid}", expect=200)
    return body["detail"]["requirement"]["status"]


def scenario_s1_normal(clients: dict, mock: Client, ck: Checks) -> None:
    """S1 正常交付：创建 → 生成 → 设计通过 → 需求人确认 → 运营查询完成记录。"""
    print("\n=== S1 正常交付 ===")
    set_mode(mock, "success")
    demand = clients["demand-a"]
    _, body = demand.request("POST", "/api/requirements", {
        "activity_name": "S1 正常交付活动",
        "purpose": "新品主视觉",
        "description": "S1 夏日冰饮主视觉，冷色调，柠檬片与透明杯",
    }, expect=201)
    rid = body["requirement"]["id"]
    _, gen = demand.request("POST", f"/api/requirements/{rid}/generations", {"request_no": f"s1-{rid}"}, expect=202)
    ck.check("S1 生成任务已受理", gen["task"]["status"] == "processing", f"task={gen['task']['id']}")
    ok = wait_for(lambda: requirement_status(demand, rid) == "candidate_pending")
    ck.check("S1 收到回调并生成候选", ok, f"status={requirement_status(demand, rid)}")
    _, detail = demand.request("GET", f"/api/requirements/{rid}", expect=200)
    ck.check("S1 当前候选存在", detail["detail"]["current_candidate"] is not None)
    ck.check("S1 描述快照已保存",
             detail["detail"]["tasks"][0]["description_snapshot"] == "S1 夏日冰饮主视觉，冷色调，柠檬片与透明杯")
    review = clients["review-a"]
    review.request("POST", f"/api/requirements/{rid}/review", {"approved": True, "reason": "构图符合文案"}, expect=200)
    ck.check("S1 复核通过后进入待确认", requirement_status(demand, rid) == "approved")
    demand.request("POST", f"/api/requirements/{rid}/adopt", {}, expect=200)
    ck.check("S1 确认采用后交付完成", requirement_status(demand, rid) == "delivered")
    ops = clients["ops-a"]
    _, overview = ops.request("GET", "/api/overview", expect=200)
    ck.check("S1 运营可查到完成记录", overview["overview"]["requirement_counts"].get("delivered", 0) >= 1,
             f"delivered={overview['overview']['requirement_counts'].get('delivered', 0)}")


def scenario_s2_rework(clients: dict, mock: Client, ck: Checks) -> None:
    """S2 业务重做：复核不通过 → 改描述再生成 → 新版本确认，旧版本不可交付。"""
    print("\n=== S2 业务重做 ===")
    set_mode(mock, "success")
    demand = clients["demand-a"]
    _, body = demand.request("POST", "/api/requirements", {
        "activity_name": "S2 业务重做活动",
        "purpose": "活动主视觉",
        "description": "S2 初版描述：红色背景促销海报",
    }, expect=201)
    rid = body["requirement"]["id"]
    demand.request("POST", f"/api/requirements/{rid}/generations", {"request_no": f"s2-a-{rid}"}, expect=202)
    wait_for(lambda: requirement_status(demand, rid) == "candidate_pending")
    review = clients["review-a"]
    review.request("POST", f"/api/requirements/{rid}/review",
                   {"approved": False, "reason": "图片不符合文案"}, expect=200)
    ck.check("S2 复核不通过需写原因且状态为待重做", requirement_status(demand, rid) == "rejected")
    # 修改描述后再次生成
    demand.request("POST", f"/api/requirements/{rid}/description",
                   {"description": "S2 第二版描述：蓝色清爽风格"}, expect=200)
    demand.request("POST", f"/api/requirements/{rid}/generations", {"request_no": f"s2-b-{rid}"}, expect=202)
    wait_for(lambda: requirement_status(demand, rid) == "candidate_pending")
    _, detail = demand.request("GET", f"/api/requirements/{rid}", expect=200)
    ck.check("S2 第二次生成后旧候选变为历史",
             detail["detail"]["current_candidate"]["attempt_no"] == 2 and len(detail["detail"]["candidates"]) == 2)
    ck.check("S2 历史描述快照未被覆盖",
             detail["detail"]["tasks"][0]["description_snapshot"] == "S2 初版描述：红色背景促销海报"
             and detail["detail"]["tasks"][1]["description_snapshot"] == "S2 第二版描述：蓝色清爽风格")
    # 历史候选不能再复核：显式指定历史候选 ID，必须被明确拒绝
    history_id = [c["id"] for c in detail["detail"]["candidates"] if not c["is_current"]][0]
    status, _ = review.request("POST", f"/api/requirements/{rid}/review",
                               {"approved": True, "reason": "试图复核历史版本", "candidate_id": history_id})
    ck.check("S2 旧版本不能被误复核", status == 409, f"status={status}")
    # 复核通过后，历史候选也不能被确认采用
    review.request("POST", f"/api/requirements/{rid}/review", {"approved": True, "reason": "第二版通过"}, expect=200)
    status, _ = demand.request("POST", f"/api/requirements/{rid}/adopt", {"candidate_id": history_id})
    ck.check("S2 旧版本不能被误确认采用", status == 409, f"status={status}")
    demand.request("POST", f"/api/requirements/{rid}/adopt", {}, expect=200)
    ck.check("S2 新版本确认采用后交付完成", requirement_status(demand, rid) == "delivered")


def scenario_s3_failure(clients: dict, mock: Client, ck: Checks) -> None:
    """S3 技术失败：拒绝 / 失败 / 超时 → 重试 → 历史原因可追溯。"""
    print("\n=== S3 技术失败 ===")
    demand = clients["demand-a"]
    # 3.1 供应商拒绝受理
    set_mode(mock, "reject")
    _, body = demand.request("POST", "/api/requirements", {
        "activity_name": "S3 技术失败活动",
        "purpose": "失败链路验证",
        "description": "S3 用于验证失败与重试的描述文本",
    }, expect=201)
    rid = body["requirement"]["id"]
    status, _ = demand.request("POST", f"/api/requirements/{rid}/generations", {"request_no": f"s3-reject-{rid}"})
    ck.check("S3 供应商拒绝时发起被拒并返回明确原因", status == 409, f"status={status}")
    ck.check("S3 拒绝后需求状态为失败可重试", requirement_status(demand, rid) == "failed")
    _, detail = demand.request("GET", f"/api/requirements/{rid}", expect=200)
    ck.check("S3 拒绝原因可追溯", "拒绝" in detail["detail"]["tasks"][0]["failure_reason"],
             detail["detail"]["tasks"][0]["failure_reason"])
    ck.check("S3 拒绝未产生候选", len(detail["detail"]["candidates"]) == 0)
    # 3.2 生成失败
    set_mode(mock, "failure")
    demand.request("POST", f"/api/requirements/{rid}/generations", {"request_no": f"s3-fail-{rid}"}, expect=202)
    wait_for(lambda: requirement_status(demand, rid) == "failed")
    _, detail = demand.request("GET", f"/api/requirements/{rid}", expect=200)
    ck.check("S3 失败回调不产生候选", len(detail["detail"]["candidates"]) == 0)
    ck.check("S3 失败原因已留痕", "失败" in detail["detail"]["tasks"][1]["failure_reason"])
    # 3.3 超时
    set_mode(mock, "timeout")
    demand.request("POST", f"/api/requirements/{rid}/generations", {"request_no": f"s3-timeout-{rid}"}, expect=202)
    ok = wait_for(lambda: requirement_status(demand, rid) == "exhausted", timeout=25)
    ck.check("S3 第三次未成功后达到次数上限", ok, f"status={requirement_status(demand, rid)}")
    _, detail = demand.request("GET", f"/api/requirements/{rid}", expect=200)
    ck.check("S3 三次历史均可追溯", len(detail["detail"]["tasks"]) == 3)
    status, _ = demand.request("POST", f"/api/requirements/{rid}/generations", {"request_no": f"s3-fourth-{rid}"})
    ck.check("S3 第四次发起被拒绝", status == 409, f"status={status}")


def scenario_s4_withdraw(clients: dict, mock: Client, ck: Checks) -> None:
    """S4 中途撤销：生成中撤销 → 供应商迟到成功 → 需求保持撤销，运营可查处理原因。"""
    print("\n=== S4 中途撤销 ===")
    set_mode(mock, "timeout")
    demand = clients["demand-a"]
    _, body = demand.request("POST", "/api/requirements", {
        "activity_name": "S4 中途撤销活动",
        "purpose": "撤销链路验证",
        "description": "S4 用于验证撤销与迟到回调的描述文本",
    }, expect=201)
    rid = body["requirement"]["id"]
    _, gen = demand.request("POST", f"/api/requirements/{rid}/generations", {"request_no": f"s4-{rid}"}, expect=202)
    task_id = gen["task"]["id"]
    demand.request("POST", f"/api/requirements/{rid}/withdraw", {}, expect=200)
    ck.check("S4 撤销后需求保持撤销", requirement_status(demand, rid) == "withdrawn")
    # 供应商迟到成功
    mock.request("POST", "/control/redeliver", {"ref": task_id, "status": "succeeded"}, expect=200)
    time.sleep(1.5)
    ck.check("S4 迟到成功后需求仍为撤销", requirement_status(demand, rid) == "withdrawn")
    _, detail = demand.request("GET", f"/api/requirements/{rid}", expect=200)
    ck.check("S4 迟到结果未形成候选", len(detail["detail"]["candidates"]) == 0)
    kinds = [e["kind"] for e in detail["detail"]["events"]]
    ck.check("S4 迟到结果保留审计", "late_result_ignored" in kinds, f"events={kinds}")
    ops = clients["ops-a"]
    _, anomalies = ops.request("GET", "/api/anomalies", expect=200)
    ck.check("S4 运营异常查询可见迟到结果",
             any(a["kind"] == "late_result_ignored" for a in anomalies["items"]))


def scenario_s5_concurrency(clients: dict, mock: Client, ck: Checks) -> None:
    """S5 并发与权限：重复发起、旧回调晚到、重复复核、他人需求访问、第三次后再次重做。"""
    print("\n=== S5 并发与权限 ===")
    set_mode(mock, "timeout")
    demand = clients["demand-a"]
    other = clients["demand-b"]
    _, body = demand.request("POST", "/api/requirements", {
        "activity_name": "S5 并发与权限活动",
        "purpose": "并发与越权验证",
        "description": "S5 用于验证并发与越权的描述文本",
    }, expect=201)
    rid = body["requirement"]["id"]
    # 5.1 同一请求编号重复发起：只创建一个供应商任务
    demand.request("POST", f"/api/requirements/{rid}/generations", {"request_no": "s5-same"}, expect=202)
    _, again = demand.request("POST", f"/api/requirements/{rid}/generations", {"request_no": "s5-same"}, expect=202)
    ck.check("S5 同请求编号重复发起返回同一任务", again["task"]["id"] is not None)
    _, detail = demand.request("GET", f"/api/requirements/{rid}", expect=200)
    ck.check("S5 同请求编号只创建一个生成任务", len(detail["detail"]["tasks"]) == 1,
             f"tasks={len(detail['detail']['tasks'])}")
    # 5.2 不同请求编号并发发起：第二个被明确拒绝
    status, _ = demand.request("POST", f"/api/requirements/{rid}/generations", {"request_no": "s5-other"})
    ck.check("S5 已有处理中任务时再次发起被拒", status == 409, f"status={status}")
    # 5.3 他人需求访问被拒
    status, _ = other.request("GET", f"/api/requirements/{rid}")
    ck.check("S5 需求人访问他人需求被拒", status == 403, f"status={status}")
    status, _ = other.request("POST", f"/api/requirements/{rid}/generations", {"request_no": "s5-cross"})
    ck.check("S5 需求人对他人需求发起生成被拒", status == 403, f"status={status}")
    # 5.4 运营无权确认采用
    ops = clients["ops-a"]
    status, _ = ops.request("POST", f"/api/requirements/{rid}/adopt", {})
    ck.check("S5 运营无权代替需求人确认采用", status in (403, 409), f"status={status}")
    # 5.5 复核人无权发起生成
    review = clients["review-a"]
    status, _ = review.request("POST", f"/api/requirements/{rid}/generations", {"request_no": "s5-review"})
    ck.check("S5 复核人无权发起生成", status == 403, f"status={status}")
    # 5.6 重复复核得到明确结果
    set_mode(mock, "success")
    _, body2 = demand.request("POST", "/api/requirements", {
        "activity_name": "S5 重复复核活动",
        "purpose": "重复复核验证",
        "description": "S5 用于验证重复复核的描述文本",
    }, expect=201)
    rid2 = body2["requirement"]["id"]
    demand.request("POST", f"/api/requirements/{rid2}/generations", {"request_no": f"s5-dup-{rid2}"}, expect=202)
    wait_for(lambda: requirement_status(demand, rid2) == "candidate_pending")
    review.request("POST", f"/api/requirements/{rid2}/review", {"approved": True, "reason": "首次复核通过"}, expect=200)
    status, _ = review.request("POST", f"/api/requirements/{rid2}/review", {"approved": True, "reason": "重复复核"})
    ck.check("S5 重复复核得到明确拒绝结果", status == 409, f"status={status}")
    # 5.7 撤销后仍可查到审计
    demand.request("POST", f"/api/requirements/{rid}/withdraw", {}, expect=200)
    ck.check("S5 撤销后仍可查到审计", requirement_status(demand, rid) == "withdrawn")


def main() -> int:
    parser = argparse.ArgumentParser(description="题目 06 五组完整业务场景验证")
    parser.add_argument("--base", default="http://127.0.0.1:8080")
    parser.add_argument("--mock", default="http://127.0.0.1:8090")
    args = parser.parse_args()

    clients = {
        "demand-a": Client(args.base).login("demand-a"),
        "demand-b": Client(args.base).login("demand-b"),
        "review-a": Client(args.base).login("review-a"),
        "ops-a": Client(args.base).login("ops-a"),
    }
    mock = Client(args.mock)
    ck = Checks()
    scenario_s1_normal(clients, mock, ck)
    scenario_s2_rework(clients, mock, ck)
    scenario_s3_failure(clients, mock, ck)
    scenario_s4_withdraw(clients, mock, ck)
    scenario_s5_concurrency(clients, mock, ck)

    print(f"\n通过 {ck.passed} 项，失败 {len(ck.failed)} 项")
    if ck.failed:
        print("失败项：" + "、".join(ck.failed))
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
