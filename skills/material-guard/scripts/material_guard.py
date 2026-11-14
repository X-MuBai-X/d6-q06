#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""material-guard 的判定脚本：只读检查营销素材需求的状态与版本不变量。

用法：
    python skills/material-guard/scripts/material_guard.py --repo . --db data/studio.db

输出：
    <out>.md   Markdown 报告
    <out>.json 结构化结果
退出码：0=PASS，1=FAIL，2=待补证（环境不可用）
"""
from __future__ import annotations

import argparse
import datetime
import json
import pathlib
import re
import sqlite3
import subprocess
import sys


def decode_text(raw: bytes) -> str:
    """按 BOM 与常见编码解码文本（Windows 日志常见 UTF-16LE，避免读成乱码导致假结论）。"""
    if raw.startswith(b"\xff\xfe") or raw.startswith(b"\xfe\xff"):
        return raw.decode("utf-16", errors="replace")
    if raw.startswith(b"\xef\xbb\xbf"):
        return raw.decode("utf-8-sig", errors="replace")
    for encoding in ("utf-8", "gb18030"):
        try:
            return raw.decode(encoding)
        except UnicodeDecodeError:
            continue
    return raw.decode("utf-8", errors="replace")


def read_text(path: pathlib.Path) -> str:
    try:
        return decode_text(path.read_bytes())
    except OSError:
        return ""


def open_db(path: pathlib.Path) -> sqlite3.Connection:
    conn = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    conn.row_factory = sqlite3.Row
    return conn


def check_db(conn: sqlite3.Connection) -> list[dict]:
    """执行 G1–G9 数据库不变量检查。"""
    results: list[dict] = []

    def add(cid: str, rule: str, name: str, ok: bool | None, evidence: str) -> None:
        status = "待补证" if ok is None else ("PASS" if ok else "FAIL")
        results.append(dict(id=cid, rule=rule, name=name, status=status, evidence=evidence))

    # G1：成功任务必须有非空描述快照；同一需求内不同尝试的快照应可区分
    rows = conn.execute(
        "SELECT id, requirement_id, attempt_no, description_snapshot FROM generation_tasks "
        "WHERE status = 'succeeded'").fetchall()
    bad = [r["id"] for r in rows if not (r["description_snapshot"] or "").strip()]
    add("G1", "R1", "成功任务的描述快照非空", not bad,
        "检查 %d 条成功任务；异常记录：%s" % (len(rows), bad if bad else "无"))

    # G2：同一需求至多一个 processing 任务；request_no 唯一
    dup_processing = conn.execute(
        "SELECT requirement_id, COUNT(*) c FROM generation_tasks WHERE status='processing' "
        "GROUP BY requirement_id HAVING c > 1").fetchall()
    dup_request = conn.execute(
        "SELECT request_no, COUNT(*) c FROM generation_tasks GROUP BY request_no HAVING c > 1").fetchall()
    add("G2", "R2", "同一需求至多一个处理中任务且请求编号唯一",
        not dup_processing and not dup_request,
        "重复处理中：%s；重复请求编号：%s" %
        ([r["requirement_id"] for r in dup_processing], [r["request_no"] for r in dup_request]))

    # G3：生成次数上限；失败类任务不得产出候选
    over = conn.execute(
        "SELECT requirement_id, COUNT(*) c FROM generation_tasks GROUP BY requirement_id HAVING c > 3").fetchall()
    bad_cand = conn.execute(
        "SELECT t.id FROM generation_tasks t JOIN candidates c ON c.task_id = t.id "
        "WHERE t.status IN ('failed','rejected','timeout')").fetchall()
    add("G3", "R3", "生成次数不超过三次且失败类任务不产出候选", not over and not bad_cand,
        "超限需求：%s；失败任务带候选：%s" %
        ([r["requirement_id"] for r in over], [r["id"] for r in bad_cand]))

    # G4：成功任务恰好一个候选；同一需求至多一个当前候选
    # 说明：需求已撤销时的迟到成功结果**不应**产生候选（题面 R7），
    # 因此「成功任务必有候选」这条不变量只适用于未撤销的需求。
    multi = conn.execute(
        "SELECT task_id, COUNT(*) c FROM candidates GROUP BY task_id HAVING c > 1").fetchall()
    miss = conn.execute(
        "SELECT t.id FROM generation_tasks t JOIN requirements r ON r.id = t.requirement_id "
        "LEFT JOIN candidates c ON c.task_id = t.id "
        "WHERE t.status='succeeded' AND r.status <> 'withdrawn' AND c.id IS NULL").fetchall()
    multi_current = conn.execute(
        "SELECT requirement_id, COUNT(*) c FROM candidates WHERE is_current=1 GROUP BY requirement_id HAVING c > 1").fetchall()
    add("G4", "R4/R7", "成功任务恰好一个候选且当前候选唯一（撤销需求的迟到结果除外）",
        not multi and not miss and not multi_current,
        "多候选任务：%s；缺候选任务：%s；多当前候选需求：%s" %
        ([r["task_id"] for r in multi], [r["id"] for r in miss], [r["requirement_id"] for r in multi_current]))

    # G5：已交付需求必须有 approved 的当前候选
    bad_delivered = conn.execute(
        "SELECT r.id FROM requirements r LEFT JOIN candidates c ON c.id = r.current_candidate "
        "WHERE r.status = 'delivered' AND (c.id IS NULL OR c.review_status <> 'approved')").fetchall()
    add("G5", "R5", "已确认采用的需求必须有复核通过的当前候选", not bad_delivered,
        "异常需求：%s" % ([r["id"] for r in bad_delivered],))

    # G6：历史候选不得仍处于待复核
    stale = conn.execute(
        "SELECT id FROM candidates WHERE is_current = 0 AND review_status = 'pending'").fetchall()
    add("G6", "R6", "历史候选不再处于待复核状态", not stale,
        "异常候选：%s" % ([r["id"] for r in stale],))

    # G7：撤销需求不得有候选；撤销后不得出现采用记录
    withdrawn_with_cand = conn.execute(
        "SELECT r.id FROM requirements r JOIN candidates c ON c.requirement_id = r.id "
        "WHERE r.status = 'withdrawn'").fetchall()
    adopted_after_withdraw = conn.execute(
        "SELECT r.id FROM requirements r JOIN events e ON e.requirement_id = r.id "
        "WHERE r.status = 'withdrawn' AND e.kind = 'requirement_adopted'").fetchall()
    add("G7", "R7", "撤销的需求没有候选也没有采用记录",
        not withdrawn_with_cand and not adopted_after_withdraw,
        "带候选的撤销需求：%s；撤销后仍被采用的：%s" %
        ([r["id"] for r in withdrawn_with_cand], [r["id"] for r in adopted_after_withdraw]))

    # G8：需求必须有创建事件；每个生成任务至少一条审计事件
    no_created = conn.execute(
        "SELECT r.id FROM requirements r LEFT JOIN events e ON e.requirement_id = r.id "
        "AND e.kind = 'requirement_created' WHERE e.id IS NULL").fetchall()
    no_event = conn.execute(
        "SELECT t.id FROM generation_tasks t LEFT JOIN events e ON e.task_id = t.id "
        "WHERE e.id IS NULL").fetchall()
    add("G8", "R8", "需求与生成任务均有审计留痕", not no_created and not no_event,
        "缺创建事件的需求：%s；缺事件的任务：%s" %
        ([r["id"] for r in no_created], [r["id"] for r in no_event]))

    # G9：撤销后不得复活（D-07 根因：G1–G8 只看候选/采用记录，不看状态与事件）
    # (a) 已撤销（存在 requirement_withdrawn 事件）的需求，状态不得被改写为
    #     generating / failed / exhausted；
    # (b) 撤销事件之后不得再出现把需求往前推的事件（发起生成、超时/失败/拒绝回调、
    #     产出候选、复核、采用）。
    revived = conn.execute(
        "SELECT r.id, r.status FROM requirements r WHERE r.status IN ('generating','failed','exhausted') "
        "AND EXISTS (SELECT 1 FROM events w WHERE w.requirement_id = r.id AND w.kind = 'requirement_withdrawn')"
    ).fetchall()
    forward_kinds = ("generation_started", "generation_timeout", "generation_failed",
                     "generation_rejected", "candidate_created", "candidate_reviewed",
                     "requirement_adopted")
    placeholders = ",".join("?" for _ in forward_kinds)
    pushed = conn.execute(
        "SELECT r.id, e.kind, e.id FROM requirements r "
        "JOIN events e ON e.requirement_id = r.id "
        "WHERE e.kind IN (%s) AND EXISTS ("
        "  SELECT 1 FROM events w WHERE w.requirement_id = r.id AND w.kind = 'requirement_withdrawn' "
        "  AND w.id < e.id)" % placeholders, forward_kinds).fetchall()
    add("G9", "R7", "撤销的需求不得复活（状态不得被改写，撤销后不得再出现推进事件）",
        not revived and not pushed,
        "撤销后状态被改写的需求：%s；撤销后仍出现推进事件：%s" %
        ([f"{r['id']}→{r['status']}" for r in revived],
         [f"{r['id']}:{r['kind']}" for r in pushed]))
    return results


def check_source(repo: pathlib.Path) -> list[dict]:
    """核对关键常量与状态判定是否与题面一致。"""
    results: list[dict] = []

    def add(cid: str, name: str, ok: bool, evidence: str) -> None:
        results.append(dict(id=cid, rule="题面一致性", name=name,
                            status="PASS" if ok else "FAIL", evidence=evidence))

    rules = (repo / "internal" / "domain" / "rules.go")
    source = read_text(rules) if rules.exists() else ""
    max_attempts = re.search(r"MaxAttempts\s*=\s*(\d+)", source)
    timeout = re.search(r"GenerationTimeout\s*=\s*(\d+)\s*\*\s*time\.Second", source)
    add("S1", "最大生成次数为 3", bool(max_attempts) and max_attempts.group(1) == "3",
        f"internal/domain/rules.go MaxAttempts = {max_attempts.group(1) if max_attempts else '未找到'}")
    add("S2", "生成超时上限为 60 秒", bool(timeout) and timeout.group(1) == "60",
        f"internal/domain/rules.go GenerationTimeout = {timeout.group(1) if timeout else '未找到'} 秒")

    checks = {
        "CanStartGeneration": ["ReqDraft", "ReqFailed", "ReqRejected", "ReqApproved"],
        "CanReview": ["ReqCandidatePending"],
        "CanAdopt": ["ReqApproved"],
    }
    for func, expected in checks.items():
        match = re.search(rf"func {func}\(status RequirementStatus\) bool \{{(.*?)\n\}}", source, re.S)
        body = match.group(1) if match else ""
        missing = [s for s in expected if s not in body]
        add(f"S3-{func}", f"{func} 的允许状态集合符合题面", not missing and bool(match),
            f"internal/domain/rules.go {func}：缺少 {missing if missing else '无'}")

    # S3 增补（D-07 根因之一）：只核对 domain/rules.go 里的**声明**不够，
    # 还要确认业务层真的**调用**了 CanStartGeneration，否则撤销过的需求仍可被复活（D-01）。
    # 判据基于语义（是否存在调用点），不依赖具体行号。
    service_dir = repo / "internal" / "service"
    call_sites: list[str] = []
    service_text: dict[str, str] = {}
    if service_dir.exists():
        for path in sorted(service_dir.glob("*.go")):
            if path.name.endswith("_test.go"):
                continue  # 测试文件不算业务实现
            text = read_text(path)
            service_text[path.name] = text
            for number, line in enumerate(text.splitlines(), start=1):
                if re.search(r"(?<!func\s)\bCanStartGeneration\s*\(", line):
                    call_sites.append(f"internal/service/{path.name}:{number}")
    add("S3-CallCanStartGeneration", "业务层存在 CanStartGeneration 的调用点（不只看声明）",
        bool(call_sites),
        "调用点：%s" % (call_sites if call_sites else "未发现（internal/service 下无任何 CanStartGeneration 调用）"))

    # 进一步对准 R7/D-01：发起生成的事务体内必须调用该判定，
    # 否则「撤销后再次发起」不会被拦下。
    start_gen = service_text.get("requirement.go", "")
    body = ""
    match = re.search(r"func \(s \*Service\) StartGeneration\(.*?\n\}\n", start_gen, re.S)
    if match:
        body = match.group(0)
    in_body = bool(re.search(r"(?<!func\s)\bCanStartGeneration\s*\(", body))
    add("S3-CallInStartGeneration", "StartGeneration 内调用 CanStartGeneration（撤销后不得再发起）",
        in_body,
        "internal/service/requirement.go 的 StartGeneration：%s" %
        ("已调用 CanStartGeneration" if in_body else "未发现 CanStartGeneration 调用（撤销后仍可被复活）"))
    return results


def run_coverage(repo: pathlib.Path, min_cov: float) -> tuple[str, str]:
    """调用统一的覆盖率门禁脚本。"""
    script = repo / "scripts" / "coverage_gate.py"
    if not script.exists():
        return "待补证", f"未找到覆盖率脚本 {script}"
    proc = subprocess.run([sys.executable, str(script), "--root", str(repo),
                           "--out-dir", "evidence", "--min", str(min_cov)],
                          capture_output=True, text=True, encoding="utf-8", errors="replace")
    summary = (repo / "evidence" / "coverage-summary.txt")
    detail = read_text(summary) if summary.exists() else (proc.stdout or "")
    return ("PASS" if proc.returncode == 0 else "FAIL"), detail


def main() -> int:
    parser = argparse.ArgumentParser(description="营销素材业务不变量检查")
    parser.add_argument("--repo", default=".", help="仓库根目录")
    parser.add_argument("--db", default="", help="SQLite 数据库文件")
    parser.add_argument("--min-coverage", type=float, default=85.0)
    parser.add_argument("--out", default="")
    parser.add_argument("--skip-tests", action="store_true")
    args = parser.parse_args()

    repo = pathlib.Path(args.repo).resolve()
    db_path = pathlib.Path(args.db).resolve() if args.db else repo / "data" / "studio.db"
    out_path = pathlib.Path(args.out).resolve() if args.out else repo / "evidence" / "material-guard.md"
    out_path.parent.mkdir(parents=True, exist_ok=True)

    lines = ["# material-guard 检查报告", ""]
    payload = {"status": "PASS", "db": str(db_path), "checks": [], "coverage": {}, "generated_at": ""}
    payload["generated_at"] = datetime.datetime.now(datetime.timezone.utc).isoformat()

    if not db_path.exists():
        lines += [f"- 判定：**待补证**", f"- 原因：数据库文件不存在（{db_path}），无法执行不变量检查。", ""]
        payload["status"] = "待补证"
        out_path.write_text("\n".join(lines), encoding="utf-8")
        out_path.with_suffix(".json").write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")
        print("\n".join(lines))
        return 2

    conn = open_db(db_path)
    try:
        db_checks = check_db(conn)
    finally:
        conn.close()
    src_checks = check_source(repo)
    payload["checks"] = db_checks + src_checks

    cov_status, cov_detail = ("待补证", "未执行") if args.skip_tests else run_coverage(repo, args.min_coverage)
    payload["coverage"] = {"status": cov_status, "report": cov_detail}

    failures = [c for c in payload["checks"] if c["status"] == "FAIL"]
    # 覆盖率门禁未执行（待补证）时，整体判定不得是 PASS：
    # 「没做」与「做不了」必须分开陈述（AGENTS.md §8 第 2 条）。
    if failures or cov_status == "FAIL":
        overall = "FAIL"
    elif cov_status == "待补证":
        overall = "待补证"
    else:
        overall = "PASS"
    payload["status"] = overall

    lines += [f"- 判定：**{overall}**", f"- 数据库：`{db_path}`", f"- 覆盖率门禁：{cov_status}", ""]
    lines += ["## 一、数据库不变量", "| 编号 | 规则 | 检查项 | 结论 | 证据 |", "| --- | --- | --- | --- | --- |"]
    for item in db_checks:
        lines.append(f"| {item['id']} | {item['rule']} | {item['name']} | {item['status']} | {item['evidence']} |")
    lines += ["", "## 二、源码一致性", "| 编号 | 检查项 | 结论 | 证据 |", "| --- | --- | --- | --- |"]
    for item in src_checks:
        lines.append(f"| {item['id']} | {item['name']} | {item['status']} | {item['evidence']} |")
    lines += ["", "## 三、覆盖率", "```", cov_detail.strip(), "```"]
    lines += ["", "## 四、失败项与建议"]
    if failures:
        for item in failures:
            lines.append(f"- **{item['id']}（{item['rule']}）**：{item['name']} —— {item['evidence']}")
    else:
        lines.append("- 无失败项。")
    if overall == "待补证":
        lines.append(f"- **整体判定为「待补证」**：覆盖率门禁状态为「{cov_status}」，"
                     "覆盖率是本 Skill 的判定项之一，未执行时不得判为 PASS；"
                     "请在具备 Go 工具链的环境重跑（去掉 --skip-tests）。")
    lines.append("")

    out_path.write_text("\n".join(lines), encoding="utf-8")
    out_path.with_suffix(".json").write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")
    print("\n".join(lines))
    if overall == "PASS":
        return 0
    return 1 if overall == "FAIL" else 2


if __name__ == "__main__":
    sys.exit(main())
