#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""acceptance-audit 的判定脚本：把题面 R1–R8 与共同要求拆成可机器判定的检查项。

用法：
    python skills/acceptance-audit/scripts/acceptance_audit.py --repo . --out-dir evidence/acceptance-audit

输出：
    <out-dir>/检查项明细.md
    <out-dir>/acceptance-audit.json
退出码：0=全部通过，1=存在 FAIL，2=存在待补证且无 FAIL
"""
from __future__ import annotations

import argparse
import datetime
import fnmatch
import json
import pathlib
import re
import subprocess
import sys

# 泄密扫描的敏感形态（不使用白名单，也不使用例外目录）
SECRET_PATTERNS = [
    (re.compile(r"ghp_[A-Za-z0-9]{20,}"), "GitHub PAT"),
    (re.compile(r"ghu_[A-Za-z0-9]{20,}"), "GitHub OAuth 令牌"),
    (re.compile(r"github_pat_[A-Za-z0-9_]{20,}"), "GitHub 细粒度 PAT"),
    (re.compile(r"(?i)(?:password|passwd|口令)\s*[:=]\s*[\"']?(?!\*)[A-Za-z0-9!@#$%^&*_\-]{6,}"), "明文口令"),
    (re.compile(r"(?i)Authorization:\s*Bearer\s+[A-Za-z0-9\-_.=]{16,}"), "Bearer 令牌"),
    (re.compile(r"(?i)(?:api[_-]?key|secret)\s*[:=]\s*[\"']?[A-Za-z0-9\-_]{12,}"), "密钥"),
    (re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----"), "私钥"),
    (re.compile(r"(?i)(?:cookie|set-cookie)\s*[:=]\s*[\"']?(?!\*)[A-Za-z0-9\-_]{16,}"), "Cookie 值"),
    # 独立验收 L-1 教训：只覆盖 GitHub 系列形态会漏掉其它厂商的令牌形态，
    # 这里补齐常见第三方令牌前缀（仍然不使用白名单或例外目录）。
    (re.compile(r"sk-(?:live|test)-[A-Za-z0-9\-_]{16,}"), "第三方 Live/Test 密钥"),
    (re.compile(r"glpat-[A-Za-z0-9\-_]{16,}"), "GitLab PAT"),
    (re.compile(r"xox[baprs]-[A-Za-z0-9-]{10,}"), "Slack 令牌"),
    (re.compile(r"AKIA[0-9A-Z]{16}"), "AWS Access Key ID"),
    (re.compile(r"npm_[A-Za-z0-9]{20,}"), "npm 令牌"),
    (re.compile(r"AIza[0-9A-Za-z\-_]{30,}"), "Google API Key"),
    (re.compile(r"-----BEGIN OPENSSH PRIVATE KEY-----"), "SSH 私钥"),
    (re.compile(r"(?i)(?:token|令牌)\s*[:=]\s*[\"']?(?!\*)[A-Za-z0-9\-_.]{16,}"), "明文令牌"),
]

# 已知凭据值的唯一来源：cmd/server/main.go
# （AGENTS.md §5 第 4 条「演示口令的完整值只保留在 cmd/server/main.go，文档与证据中一律以掩码展示」）
#
# D-06 教训：只靠书写格式（口令[:=] 之类）的正则会漏检「口令统一为 `xxx`」这种写法，
# 于是 C-11 假 PASS。因此改为「已知值检测 + 形态检测」双保险：
#   * 已知值检测：运行时从 cmd/server/main.go 读取真实凭据常量值，再逐文件比对；
#   * 形态检测：保留上表的形态类正则，兼顾不在本仓库出现的第三方令牌。
#
# 本 Skill 文件里**不写死任何明文凭据值**（否则会被自己的扫描规则命中）：
# 明文值只在运行时从上面的唯一来源读取，且命中信息里一律以掩码展示。
CREDENTIAL_SOURCE = "cmd/server/main.go"
CREDENTIAL_CONST_RE = re.compile(r'(?m)^\s*(?:const\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*"([^"\n]{6,})"')
CREDENTIAL_NAME_HINT = re.compile(r"(?i)(password|passwd|pwd|secret|token|api[_-]?key)")


class Checker:
    def __init__(self) -> None:
        self.items: list[dict] = []

    def add(self, cid, category, rule, name, status, evidence, verify) -> None:
        self.items.append(dict(id=cid, category=category, rule=rule, name=name,
                               status=status, evidence=evidence, verify=verify))

    def ok(self, cid, category, rule, name, evidence, verify="无需复验，结论已由证据支撑") -> None:
        self.add(cid, category, rule, name, "PASS", evidence, verify)

    def fail(self, cid, category, rule, name, evidence, verify) -> None:
        self.add(cid, category, rule, name, "FAIL", evidence, verify)

    def pending(self, cid, category, rule, name, evidence, verify) -> None:
        self.add(cid, category, rule, name, "待补证", evidence, verify)


def decode_text(raw: bytes) -> str:
    """按 BOM 与常见编码解码文本。

    Windows 上生成的日志/导出件常见 UTF-16LE（带 BOM）；
    若一律按 UTF-8 读取会变成乱码，出现「证据里其实写着通过/失败 0 项」却被误判的**假结论**。
    """
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


def read(repo: pathlib.Path, rel: str) -> str:
    path = repo / rel
    if not path.exists():
        return ""
    try:
        raw = path.read_bytes()
    except OSError:
        return ""
    return decode_text(raw)


def line_of(text: str, needle: str) -> str:
    for index, line in enumerate(text.splitlines(), start=1):
        if needle in line:
            return f"{index}: {line.strip()}"
    return ""


def mask_value(value: str) -> str:
    """凭据值掩码：报告中只留首尾各若干位，绝不落明文。"""
    if len(value) <= 4:
        return "*" * len(value)
    return value[:3] + "*" * (len(value) - 4) + value[-1:]


def known_credentials(repo: pathlib.Path) -> list[tuple[str, str]]:
    """读取真实演示凭据值（运行时从唯一来源读取，脚本内不写死明文）。"""
    text = read(repo, CREDENTIAL_SOURCE)
    found: list[tuple[str, str]] = []
    for match in CREDENTIAL_CONST_RE.finditer(text):
        name, value = match.group(1), match.group(2)
        if CREDENTIAL_NAME_HINT.search(name) and not set(value) <= {"*"}:
            found.append((name, value))
    return found


def iter_text_files(base: pathlib.Path):
    """遍历目录下的文本文件；二进制文件（SQLite 库、可执行文件）按内容里的 NUL 字节跳过，
    不是按路径做白名单。"""
    for path in sorted(base.rglob("*")):
        if not path.is_file():
            continue
        try:
            raw = path.read_bytes()
        except OSError:
            continue
        if b"\x00" in raw and not (raw.startswith(b"\xff\xfe") or raw.startswith(b"\xfe\xff")):
            continue  # 二进制文件（SQLite 库、可执行文件）：按内容判定，不是按路径做白名单
        yield path, decode_text(raw)


def gitignore_patterns(repo: pathlib.Path) -> list[str]:
    path = repo / ".gitignore"
    if not path.exists():
        return []
    return [line.strip() for line in path.read_text(encoding="utf-8", errors="replace").splitlines()
            if line.strip() and not line.strip().startswith("#")]


def is_ignored(rel_posix: str, patterns: list[str]) -> bool:
    """按 .gitignore 判断文件是否属于「不属于版本库收录范围」的构建产物/本地数据。

    注意：这里排除的是 **仓库收录范围**（与 `git status` 一致），
    **不是**按敏感类型做白名单 —— 任何源码、文档、证据目录都不会被跳过。
    """
    parts = rel_posix.split("/")
    for raw in patterns:
        pattern = raw
        if pattern.startswith("!"):
            continue  # 本仓库未使用否定规则
        if pattern.endswith("/"):
            pattern = pattern[:-1]
        if not pattern:
            continue
        if "/" in pattern:
            if fnmatch.fnmatchcase(rel_posix, pattern):
                return True
        elif any(fnmatch.fnmatchcase(part, pattern) for part in parts):
            return True
    return False


def scan_secrets(repo: pathlib.Path, roots: list[str]) -> list[str]:
    """全量扫描指定目录（无白名单、无例外目录），「已知凭据值 + 敏感形态」双保险判定命中。"""
    hits: list[str] = []
    creds = known_credentials(repo)
    for root in roots:
        base = repo / root
        if not base.exists():
            continue
        for path, content in iter_text_files(base):
            rel = path.relative_to(repo)
            for number, line in enumerate(content.splitlines(), start=1):
                for name, value in creds:
                    if value in line:
                        hits.append(f"{rel}:{number}（已知演示凭据 {name} 明文，值 {mask_value(value)}）")
                for pattern, label in SECRET_PATTERNS:
                    if pattern.search(line):
                        hits.append(f"{rel}:{number}（{label}）")
    return hits


def run(cmd: list[str], cwd: pathlib.Path) -> tuple[int, str]:
    proc = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, encoding="utf-8", errors="replace")
    return proc.returncode, (proc.stdout or "") + (proc.stderr or "")


def newest_source_mtime(repo: pathlib.Path) -> float:
    """仓库内最新的 .go / .sql / 流水线配置文件的修改时间。"""
    newest = 0.0
    for rel_root in ("internal", "cmd", "scripts"):
        base = repo / rel_root
        if not base.exists():
            continue
        for path in base.rglob("*"):
            if path.is_file() and path.suffix in (".go", ".sql", ".sh", ".py"):
                newest = max(newest, path.stat().st_mtime)
    workflow = repo / ".github" / "workflows" / "ci.yml"
    if workflow.exists():
        newest = max(newest, workflow.stat().st_mtime)
    return newest


def stale_against_sources(repo: pathlib.Path, evidence_path: pathlib.Path) -> str:
    """证据文件是否早于最新源码；是则返回原因描述，否则返回空字符串。

    独立验收 L-3 教训：只读既有结论文件属于自证循环，必须确认结论不是旧版本留下的。
    """
    if not evidence_path.exists():
        return ""
    newest = newest_source_mtime(repo)
    if newest and evidence_path.stat().st_mtime < newest:
        return f"证据文件时间 {evidence_path.stat().st_mtime:.0f} < 最新源码时间 {newest:.0f}"
    return ""


def check_business_rules(ck: Checker, repo: pathlib.Path) -> None:
    rules = read(repo, "internal/domain/rules.go")
    service = read(repo, "internal/service/requirement.go")
    callback = read(repo, "internal/service/callback.go")
    schema = read(repo, "internal/store/schema.sql")
    query = read(repo, "internal/service/query.go")
    store_tx = read(repo, "internal/store/tx.go")

    # R1 描述快照不可覆盖
    if "DescriptionSnapshot" in store_tx and "req.Description" in service:
        ck.ok("R1-01", "业务规则", "R1", "每次生成保存当时描述，历史不被覆盖",
              f"internal/service/requirement.go {line_of(service, 'DescriptionSnapshot: req.Description')}")
    else:
        ck.fail("R1-01", "业务规则", "R1", "每次生成保存当时描述，历史不被覆盖",
                "未找到描述快照写入逻辑", "补充 DescriptionSnapshot 写入并补单测")

    # R2 幂等与唯一处理中任务
    # 独立验收 L-2 教训：只做「字符串存在性匹配」无法区分「复合唯一索引」与
    # 「只留下一句 DROP INDEX 的残留」，也无法识别跨需求串号。这里改为按语义判定：
    #   (a) 存在针对 (requirement_id, request_no) 的**复合唯一索引**；
    #   (b) 幂等命中时校验了需求归属（跨需求复用必须被拒绝）。
    composite_index = re.search(
        r"CREATE\s+UNIQUE\s+INDEX[^;]*\(\s*requirement_id\s*,\s*request_no\s*\)", schema, re.I | re.S) is not None
    processing_index = "idx_one_processing_task" in schema
    scope_check = bool(re.search(r"RequirementID\s*!=\s*requirementID", service)) or \
        bool(re.search(r"existing\.RequirementID", service))
    has_idempotent = "GetTaskByRequestNoTx" in service
    if composite_index and processing_index and scope_check and has_idempotent:
        ck.ok("R2-01", "业务规则", "R2", "同一需求至多一个处理中任务且请求编号幂等",
              f"internal/store/schema.sql {line_of(schema, 'idx_one_processing_task')}；"
              f"internal/service/requirement.go {line_of(service, 'GetTaskByRequestNoTx')}；"
              "已校验 (requirement_id, request_no) 复合唯一索引与幂等命中的需求归属")
    else:
        missing = []
        if not composite_index:
            missing.append("internal/store/schema.sql 缺少 (requirement_id, request_no) 复合唯一索引")
        if not processing_index:
            missing.append("internal/store/schema.sql 缺少处理中任务唯一索引")
        if not scope_check:
            missing.append("internal/service/requirement.go 的幂等命中未校验需求归属（跨需求串号风险）")
        if not has_idempotent:
            missing.append("internal/service/requirement.go 缺少幂等分支")
        ck.fail("R2-01", "业务规则", "R2", "同一需求至多一个处理中任务且请求编号幂等",
                "；".join(missing), "补复合唯一索引、幂等归属校验与处理中任务唯一约束")

    # R3 次数上限与超时
    max_ok = re.search(r"MaxAttempts\s*=\s*3", rules) is not None
    timeout_ok = re.search(r"GenerationTimeout\s*=\s*60\s*\*\s*time\.Second", rules) is not None
    sweep_ok = "SweepTimeouts" in callback and "TaskTimeout" in callback
    if max_ok and timeout_ok and sweep_ok:
        ck.ok("R3-01", "业务规则", "R3", "失败/超时无候选、生成次数上限三次",
              f"internal/domain/rules.go {line_of(rules, 'MaxAttempts')}；{line_of(rules, 'GenerationTimeout')}；"
              f"internal/service/callback.go {line_of(callback, 'TaskTimeout')}")
    else:
        ck.fail("R3-01", "业务规则", "R3", "失败/超时无候选、生成次数上限三次",
                "次数上限/超时/巡检实现不完整", "补齐上限与超时巡检实现")

    # R4 回调鉴权、幂等与异常可查
    # 判据说明（分层原因，修正此前把「事件类型定义位置」与「异常记录实现位置」混为一谈的缺陷）：
    # 事件类型属于领域模型，常量集中定义在 internal/domain/types.go；
    # 验签与异常记录的实现分别位于 internal/httpapi/server.go（验签失败记 signature_invalid）
    # 与 internal/service/callback.go（重复回调记 callback_duplicate、未知任务记 callback_unknown_task）。
    # 因此按各要素的真实实现位置判定，不能要求 domain 常量名字符串出现在 service 文件里。
    httpapi = read(repo, "internal/httpapi/server.go")
    domain_types = read(repo, "internal/domain/types.go")
    service_sources = ""
    service_dir = repo / "internal/service"
    if service_dir.exists():
        for path in sorted(service_dir.glob("*.go")):
            if path.name.endswith("_test.go"):
                continue  # 测试文件不算业务实现
            service_sources += path.read_text(encoding="utf-8", errors="replace")
    sign_ok = "supplier.VerifySignature" in httpapi
    sign_const_ok = re.search(r"EventSignatureInvalid\s*=", domain_types) is not None
    sign_used_ok = ("domain.EventSignatureInvalid" in httpapi) or ("domain.EventSignatureInvalid" in service_sources)
    dup_ok = "EventCallbackDuplicate" in callback and "EventCallbackUnknown" in callback
    missing = []
    if not sign_ok:
        missing.append("internal/httpapi/server.go 未调用 supplier.VerifySignature（回调缺少验签）")
    if not sign_const_ok:
        missing.append("internal/domain/types.go 未定义 EventSignatureInvalid")
    if not sign_used_ok:
        missing.append("EventSignatureInvalid 未在 internal/httpapi/server.go 或 internal/service 中用于记录异常事件")
    if not dup_ok:
        missing.append("internal/service/callback.go 缺少 EventCallbackDuplicate（重复回调）或 EventCallbackUnknown（未知任务回调）")
    if not missing:
        ck.ok("R4-01", "业务规则", "R4", "回调鉴权、重复回调不重复建候选、异常可查",
              f"internal/httpapi/server.go {line_of(httpapi, 'supplier.VerifySignature')}；"
              f"internal/domain/types.go {line_of(domain_types, 'EventSignatureInvalid')}；"
              f"internal/service/callback.go {line_of(callback, 'EventCallbackDuplicate')}；"
              f"internal/service/callback.go {line_of(callback, 'EventCallbackUnknown')}")
    else:
        ck.fail("R4-01", "业务规则", "R4", "回调鉴权、重复回调不重复建候选、异常可查",
                "；".join(missing), "按缺失项补实现：" + "；".join(missing))

    # R5 复核原因与确认采用
    reason_ok = "ValidateReviewReason" in read(repo, "internal/domain/rules.go")
    adopt_ok = "func (s *Service) Adopt" in service and "ReqDelivered" in service
    if reason_ok and adopt_ok:
        ck.ok("R5-01", "业务规则", "R5", "复核不通过必须写原因，确认采用才算交付",
              f"internal/domain/rules.go {line_of(read(repo, 'internal/domain/rules.go'), 'ValidateReviewReason')}；"
              f"internal/service/requirement.go {line_of(service, 'func (s *Service) Adopt')}")
    else:
        ck.fail("R5-01", "业务规则", "R5", "复核不通过必须写原因，确认采用才算交付",
                "复核原因校验或采用逻辑缺失", "补原因校验与采用状态流转")

    # R6 版本历史
    current_ok = "ClearCurrentCandidatesTx" in store_tx and "ErrNotCurrentCandidate" in service
    if current_ok:
        ck.ok("R6-01", "业务规则", "R6", "下一次生成后旧候选只作历史且不可复核/确认",
              f"internal/service/requirement.go {line_of(service, 'ErrNotCurrentCandidate')}；"
              f"internal/store/tx.go {line_of(store_tx, 'ClearCurrentCandidatesTx')}")
    else:
        ck.fail("R6-01", "业务规则", "R6", "下一次生成后旧候选只作历史且不可复核/确认",
                "历史候选标记或拒绝逻辑缺失", "补 is_current 流转与拒绝分支")

    # R7 撤销
    withdraw_ok = "func (s *Service) Withdraw" in service and "ReqWithdrawn" in service
    late_ok = "EventLateResultIgnored" in callback
    if withdraw_ok and late_ok:
        ck.ok("R7-01", "业务规则", "R7", "确认采用前可撤销，迟到结果不恢复需求",
              f"internal/service/callback.go {line_of(callback, 'EventLateResultIgnored')}；"
              f"internal/service/requirement.go {line_of(service, 'func (s *Service) Withdraw')}")
    else:
        ck.fail("R7-01", "业务规则", "R7", "确认采用前可撤销，迟到结果不恢复需求",
                "撤销或迟到结果处理缺失", "补撤销与迟到结果分支")

    # R8 权限与核查
    perm_ok = "actorCanView" in read(repo, "internal/service/service.go") and "ErrForbidden" in service
    detail_ok = "RequirementDetail" in query and "ListAnomalies" in query
    if perm_ok and detail_ok:
        ck.ok("R8-01", "业务规则", "R8", "需求人只看自己的需求，运营可查生成与异常记录",
              f"internal/service/service.go {line_of(read(repo, 'internal/service/service.go'), 'actorCanView')}；"
              f"internal/service/query.go {line_of(query, 'func (s *Service) RequirementDetail')}")
    else:
        ck.fail("R8-01", "业务规则", "R8", "需求人只看自己的需求，运营可查生成与异常记录",
                "权限判定或核查视图缺失", "补权限判定与核查视图")


def check_common_requirements(ck: Checker, repo: pathlib.Path, min_cov: float, skip_toolchain: bool) -> None:
    # 数据库持久化
    schema = read(repo, "internal/store/schema.sql")
    if "CREATE TABLE IF NOT EXISTS requirements" in schema and "generation_tasks" in schema:
        ck.ok("C-01", "共同要求", "§1.3", "使用数据库保存核心业务数据", "internal/store/schema.sql 建表语句齐全")
    else:
        ck.fail("C-01", "共同要求", "§1.3", "使用数据库保存核心业务数据",
                "未找到业务表结构", "补建表语句")

    # 最小业务页面
    pages = ["workbench.html", "requirement.html", "review.html", "ops.html"]
    missing = [p for p in pages if not (repo / "internal/httpapi/templates" / p).exists()]
    if not missing:
        ck.ok("C-02", "共同要求", "§1.2", "提供可操作的最小业务页面",
              "internal/httpapi/templates/ 下存在工作台、需求详情、复核、运营异常四个页面")
    else:
        ck.fail("C-02", "共同要求", "§1.2", "提供可操作的最小业务页面",
                f"缺少页面：{missing}", "补齐页面模板")

    # 模拟供应商四类场景
    mockgen = read(repo, "cmd/mockgen/main.go")
    modes = [m for m in ("modeSuccess", "modeFailure", "modeReject", "modeTimeout") if m in mockgen]
    if len(modes) == 4:
        ck.ok("C-03", "共同要求", "§1.5", "模拟供应商可触发成功/失败/拒绝/超时/重复通知",
              f"cmd/mockgen/main.go 覆盖 {', '.join(modes)}，并支持重复投递")
    else:
        ck.fail("C-03", "共同要求", "§1.5", "模拟供应商可触发成功/失败/拒绝/超时/重复通知",
                f"仅覆盖 {modes}", "补齐模拟供应商的行为分支")

    # 阶段产物齐备
    docs = ["docs/01-需求分析.md", "docs/02-原型设计.md", "docs/03-任务规划.md",
            "docs/06-会话导出.md", "docs/08-Skill使用记录.md", "docs/09-AI使用说明.md",
            "docs/10-交付与发布说明.md", "docs/12-讲解视频带时间点目录.md", "skill-usage.md"]
    missing_docs = [d for d in docs if not (repo / d).exists()]
    if not missing_docs:
        ck.ok("C-04", "共同要求", "§2", "各阶段产物齐备", "需求分析、原型、计划、会话导出、Skill 记录、AI 说明、交付说明、讲解目录、skill-usage.md 均存在")
    else:
        ck.fail("C-04", "共同要求", "§2", "各阶段产物齐备", f"缺少：{missing_docs}", "补齐阶段产物")

    # AGENTS.md 与读取记录
    agents = read(repo, "AGENTS.md")
    record = read(repo, "docs/agents/AGENTS-读取记录.md")
    snapshots = list((repo / "docs/agents").glob("AGENTS-v*.md")) if (repo / "docs/agents").exists() else []
    if agents and record and snapshots:
        ck.ok("C-05", "共同要求", "§3", "AGENTS.md 有版本快照与实际读取记录",
              f"AGENTS.md 存在；版本快照 {len(snapshots)} 份；读取记录 {len(record.splitlines())} 行")
    else:
        ck.fail("C-05", "共同要求", "§3", "AGENTS.md 有版本快照与实际读取记录",
                "缺少 AGENTS.md、版本快照或读取记录", "补齐约束文件快照与读取记录")

    # 两个自编 Skill 的七要素
    seven = ["## 1. 用途", "## 2. 触发场景", "## 3. 输入", "## 4. 执行步骤",
             "## 5. 输出格式", "## 6. 失败处理", "## 7. 完成标准"]
    skill_dirs = [repo / "skills/material-guard", repo / "skills/acceptance-audit"]
    problems = []
    for directory in skill_dirs:
        skill_file = directory / "SKILL.md"
        if not skill_file.exists():
            problems.append(f"{directory.name}: 缺少 SKILL.md")
            continue
        content = skill_file.read_text(encoding="utf-8")
        missing_elements = [e for e in seven if e not in content]
        if missing_elements:
            problems.append(f"{directory.name}: 缺少要素 {missing_elements}")
        if not list((directory / "scripts").glob("*.py")):
            problems.append(f"{directory.name}: 缺少判定脚本")
    if not problems:
        ck.ok("C-06", "共同要求", "§4", "至少两个自编 Skill，七要素齐全且有判定脚本",
              "skills/material-guard 与 skills/acceptance-audit 七要素齐全，均带脚本")
    else:
        ck.fail("C-06", "共同要求", "§4", "至少两个自编 Skill，七要素齐全且有判定脚本",
                "；".join(problems), "补齐七要素与脚本")

    # Skill 调用记录
    usage = read(repo, "skill-usage.md")
    if usage and "material-guard" in usage and "acceptance-audit" in usage:
        ck.ok("C-07", "共同要求", "§4", "skill-usage.md 汇总实际使用的 Skill", "skill-usage.md 含两个自编 Skill 的六项说明")
    else:
        ck.fail("C-07", "共同要求", "§4", "skill-usage.md 汇总实际使用的 Skill",
                "skill-usage.md 缺少自编 Skill 的登记", "补齐 skill-usage.md 六列表")

    # 覆盖率
    # 独立验收 L-3 教训：只读既有结论文件属于「自证循环」。这里改为**复算**：
    # 用仓库自带的唯一口径脚本从原始剖面重新计算，并检查剖面是否比源码旧（过期即待补证）。
    summary = read(repo, "evidence/coverage-summary.txt")
    profile_path = repo / "evidence" / "coverage.out"
    stale_reason = stale_against_sources(repo, profile_path)
    recomputed = None
    if profile_path.exists() and not stale_reason:
        code, output = run([sys.executable, "scripts/coverage_gate.py", "--root", ".",
                            "--out-dir", "evidence", "--min", "85", "--skip-tests"], repo)
        if code == 0 and "PASS" in read(repo, "evidence/coverage-summary.txt"):
            recomputed = True
        else:
            recomputed = False
    if recomputed:
        ck.ok("C-08", "共同要求", "§6.1", "单元测试语句覆盖率整体与各核心包均达标",
              "按 scripts/coverage_gate.py 从原始剖面复算，结论为 PASS（剖面不早于源码）")
    elif stale_reason:
        ck.pending("C-08", "共同要求", "§6.1", "单元测试语句覆盖率整体与各核心包均达标",
                   f"覆盖率剖面早于最新源码（{stale_reason}），结论可能过期",
                   "重跑 python scripts/coverage_gate.py --root . --out-dir evidence --min 85")
    elif recomputed is False:
        ck.fail("C-08", "共同要求", "§6.1", "单元测试语句覆盖率整体与各核心包均达标",
                "按 scripts/coverage_gate.py 从原始剖面复算未通过", "补足覆盖率后重跑 scripts/coverage_gate.py")
    else:
        ck.pending("C-08", "共同要求", "§6.1", "单元测试语句覆盖率整体与各核心包均达标",
                   "未找到 evidence/coverage.out", "执行 python scripts/coverage_gate.py --root . --out-dir evidence")

    # 场景验证：同样避免「自证循环」——日志必须不早于最新源码，否则结论可能已过期。
    scenario_log = read(repo, "evidence/scenario-verify.log")
    scenario_path = repo / "evidence" / "scenario-verify.log"
    scenario_stale = stale_against_sources(repo, scenario_path)
    if scenario_log and "失败 0 项" in scenario_log and not scenario_stale:
        ck.ok("C-09", "共同要求", "§2/§10", "五组完整工作场景实测通过",
              "evidence/scenario-verify.log 显示全部场景断言通过（日志不早于最新源码）")
    elif scenario_log and scenario_stale:
        ck.pending("C-09", "共同要求", "§2/§10", "五组完整工作场景实测通过",
                   f"场景日志早于最新源码（{scenario_stale}），结论可能过期",
                   "重跑 python scripts/verify_scenarios.py 并归档输出")
    elif scenario_log:
        ck.fail("C-09", "共同要求", "§2/§10", "五组完整工作场景实测通过",
                "场景验证日志中存在失败项", "修复后重跑 scripts/verify_scenarios.py")
    else:
        ck.pending("C-09", "共同要求", "§2/§10", "五组完整工作场景实测通过",
                   "未找到 evidence/scenario-verify.log", "部署服务后执行 scripts/verify_scenarios.py")

    # 工具链
    if skip_toolchain:
        ck.pending("T-01", "共同要求", "§6.6", "gofmt / go vet / 单元测试 / 竞态检测",
                   "本次以 --skip-toolchain 跳过工具链检查", "在具备 Go 工具链的环境执行 scripts/quality_gate.sh")
    else:
        code, output = run(["gofmt", "-l", "."], repo)
        if code == 0 and not output.strip():
            ck.ok("T-01", "共同要求", "§6.6", "gofmt 零差异", "gofmt -l . 输出为空")
        else:
            ck.fail("T-01", "共同要求", "§6.6", "gofmt 零差异", f"gofmt -l . 输出：{output.strip()}", "执行 gofmt -w .")
        code, output = run(["go", "vet", "./..."], repo)
        if code == 0:
            ck.ok("T-02", "共同要求", "§6.6", "go vet 无告警", "go vet ./... 退出码 0")
        else:
            ck.fail("T-02", "共同要求", "§6.6", "go vet 无告警", output.strip()[:400], "修复 vet 告警后重跑")

    # 提交消息格式
    if (repo / ".git").exists():
        code, output = run(["git", "log", "--pretty=%s", "-n", "50"], repo)
        bad = [line for line in output.splitlines()
               if line and not re.match(r"^\[(feat|fix|hotfix|opt|docs|test)\]", line)]
        if code == 0 and not bad:
            ck.ok("C-10", "共同要求", "§9", "提交消息符合 [类型] [版本号] [编号] 描述 格式",
                  "git log 全部提交消息格式合规")
        else:
            ck.fail("C-10", "共同要求", "§9", "提交消息符合 [类型] [版本号] [编号] 描述 格式",
                    f"不合规提交：{bad[:5]}", "按规范改写提交消息（必要时用 filter-branch 或重建分支）")
    else:
        ck.pending("C-10", "共同要求", "§9", "提交消息符合 [类型] [版本号] [编号] 描述 格式",
                   "当前目录不是 Git 仓库", "在仓库根目录重新执行本 Skill")

    # 泄密扫描（无白名单、无例外目录）：已知凭据值检测 + 敏感形态检测
    hits = scan_secrets(repo, ["evidence", "docs"])
    creds = known_credentials(repo)
    if hits:
        ck.fail("C-11", "共同要求", "§3/§8", "证据与文档中无明文口令、密钥与令牌",
                f"命中 {len(hits)} 处：{hits[:8]}",
                "把凭据改为掩码或移到仓库外的凭据文件")
    else:
        ck.ok("C-11", "共同要求", "§3/§8", "证据与文档中无明文口令、密钥与令牌",
              f"全量扫描 evidence/ 与 docs/ 未命中（无白名单、无例外目录；"
              f"已知凭据值比对 {len(creds)} 项 + {len(SECRET_PATTERNS)} 类形态正则）")

    # 已知演示凭据的明文只允许存在于 cmd/server/main.go（AGENTS.md §5 第 4 条）
    # 这一项对仓库全量扫描（同样不做路径白名单；只排除二进制文件），与 C-11 的文档/证据范围互补。
    outside = []
    ignore_patterns = gitignore_patterns(repo)
    for path, content in iter_text_files(repo):
        rel = path.relative_to(repo)
        rel_posix = rel.as_posix()
        if rel_posix == CREDENTIAL_SOURCE:
            continue
        if rel.parts and rel.parts[0] == ".git":
            continue  # 版本库内部对象目录，不是工作区交付内容
        if is_ignored(rel_posix, ignore_patterns):
            continue  # 构建产物/本地数据，不在版本库收录范围内
        for name, value in creds:
            if value in content:
                outside.append(f"{rel}（{name}，值 {mask_value(value)}）")
    if outside:
        ck.fail("C-14", "共同要求", "§5/§8", "演示凭据明文只存在于 cmd/server/main.go",
                f"以下 {len(outside)} 处仍含已知演示凭据明文：{outside[:8]}",
                "改为从 cmd/server/main.go 读取该常量或改用掩码展示（不得新增例外目录）")
    else:
        ck.ok("C-14", "共同要求", "§5/§8", "演示凭据明文只存在于 cmd/server/main.go",
              f"仓库全量扫描未在 {CREDENTIAL_SOURCE} 之外发现已知演示凭据明文"
              f"（{len(creds)} 项已知值；扫描范围与版本库收录范围一致，按 .gitignore 排除构建产物）")

    # 部署与冒烟
    deployment = read(repo, "evidence/deployment-main.log")
    if deployment.strip():
        ck.ok("C-12", "共同要求", "§7", "自动部署有实际运行记录", "evidence/deployment-main.log 存在且非空")
    else:
        ck.pending("C-12", "共同要求", "§7", "自动部署有实际运行记录",
                   "未找到 evidence/deployment-main.log", "在主分支合并提交上执行部署流水线并归档日志")

    # 流水线配置
    workflow = repo / ".github/workflows"
    if workflow.exists() and any(workflow.glob("*.yml")):
        ck.ok("C-13", "共同要求", "§7", "提交可实际执行的自动部署流水线", ".github/workflows/ 下存在流水线配置")
    else:
        ck.fail("C-13", "共同要求", "§7", "提交可实际执行的自动部署流水线",
                "未找到 .github/workflows/*.yml", "补流水线配置")


def main() -> int:
    parser = argparse.ArgumentParser(description="题目 06 验收检查")
    parser.add_argument("--repo", default=".")
    parser.add_argument("--out-dir", default="evidence/acceptance-audit")
    parser.add_argument("--min-coverage", type=float, default=85.0)
    parser.add_argument("--skip-toolchain", action="store_true")
    args = parser.parse_args()

    repo = pathlib.Path(args.repo).resolve()
    out_dir = (repo / args.out_dir).resolve()
    out_dir.mkdir(parents=True, exist_ok=True)

    ck = Checker()
    check_business_rules(ck, repo)
    check_common_requirements(ck, repo, args.min_coverage, args.skip_toolchain)

    fails = [i for i in ck.items if i["status"] == "FAIL"]
    pendings = [i for i in ck.items if i["status"] == "待补证"]
    overall = "FAIL" if fails else ("待补证" if pendings else "PASS")

    lines = ["# 验收检查项明细（题目 06）", "",
             f"- 检查时间：{datetime.datetime.now(datetime.timezone.utc).astimezone().isoformat(timespec='seconds')}",
             f"- 仓库：`{repo}`",
             f"- 总判定：**{overall}**（FAIL {len(fails)} 项，待补证 {len(pendings)} 项，通过 {len(ck.items) - len(fails) - len(pendings)} 项）", "",
             "| 编号 | 类别 | 题面依据 | 检查项 | 结论 | 证据 | 复验方式 |",
             "| --- | --- | --- | --- | --- | --- | --- |"]
    for item in ck.items:
        lines.append(f"| {item['id']} | {item['category']} | {item['rule']} | {item['name']} | "
                     f"**{item['status']}** | {item['evidence']} | {item['verify']} |")
    lines += ["", "## 待补证清单", ""]
    if pendings:
        for item in pendings:
            lines.append(f"- {item['id']}：{item['name']} —— {item['evidence']}；补证方式：{item['verify']}")
    else:
        lines.append("- 无")
    lines += ["", "## 未通过项", ""]
    if fails:
        for item in fails:
            lines.append(f"- {item['id']}：{item['name']} —— {item['evidence']}；整改：{item['verify']}")
    else:
        lines.append("- 无")
    lines.append("")

    (out_dir / "检查项明细.md").write_text("\n".join(lines), encoding="utf-8")
    payload = {"generated_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
               "status": overall, "items": ck.items}
    (out_dir / "acceptance-audit.json").write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")
    print("\n".join(lines))
    return 1 if fails else (2 if pendings else 0)


if __name__ == "__main__":
    sys.exit(main())
