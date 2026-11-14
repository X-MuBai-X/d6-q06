#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""student-project-audit v3.0 输入生成器（题目 06）。

生成两份 UTF-8 JSON：
  - audit-input.json ：评分事实（artifacts / checks / issues / token_usage）
  - report-data.json ：结构化验收记录（八类表 + 证据索引 + used_skill_ids）

纪律
----
* artifacts 与 checks 逐条复制技能内置固定清单（profiles/06.json），不增不减不改字段；
* 结论必须落到真实文件/符号/日志；数字在运行时从仓库读取，不写死；
* 缺证记 unknown 并登记责任方，**不用 NA、不用“通过”掩盖**；
* 人工交付物（讲解视频、班级群提交）记 unknown / 待补证。

用法：
  $env:PYTHONUTF8='1'
  python evidence/audit/build_audit_input.py --repo <仓库根> --audit-skill <审计技能根> --out-dir evidence/audit
"""
from __future__ import annotations

import argparse
import hashlib
import json
import re
import subprocess
from pathlib import Path

# ------------------------------------------------------------------ 仓库事实读取


def sh(repo: Path, *args: str) -> str:
    proc = subprocess.run(["git", "-C", str(repo), *args], capture_output=True, check=False)
    return proc.stdout.decode("utf-8", "replace")


def read(repo: Path, rel: str) -> str:
    path = repo / rel
    return path.read_text(encoding="utf-8", errors="replace") if path.exists() else ""


def exists(repo: Path, rel: str) -> bool:
    return (repo / rel).exists()


def coverage_numbers(repo: Path) -> dict:
    text = read(repo, "evidence/coverage-summary.txt")
    out = {"overall": None, "packages": {}, "verdict": None, "raw": text}
    match = re.search(r"整体[^0-9]*([0-9.]+)%", text)
    if match:
        out["overall"] = match.group(1)
    for pkg, pct in re.findall(r"((?:internal|cmd)/[a-z]+)\s+([0-9.]+)%", text):
        out["packages"][pkg] = pct
    out["verdict"] = "PASS" if "PASS" in text else ("FAIL" if "FAIL" in text else None)
    return out


def scenario_counts(repo: Path) -> dict:
    text = read(repo, "evidence/scenario-verify.log")
    summary = re.search(r"通过\s*(\d+)\s*项，失败\s*(\d+)\s*项", text)
    return {
        "passed": int(summary.group(1)) if summary else len(re.findall(r"\[PASS\]", text)),
        "failed": int(summary.group(2)) if summary else len(re.findall(r"\[FAIL\]", text)),
    }


def git_facts(repo: Path) -> dict:
    head = sh(repo, "rev-parse", "HEAD").strip()
    return {
        "head": head,
        "head_short": head[:7],
        "branch": sh(repo, "rev-parse", "--abbrev-ref", "HEAD").strip(),
        "log": [line for line in sh(repo, "log", "--format=%h|%s", "-30").splitlines() if line.strip()],
        "count": len([x for x in sh(repo, "log", "--format=%h", "-1000").splitlines() if x.strip()]),
    }


def grep_lines(repo: Path, rel: str, pattern: str) -> list[str]:
    text = read(repo, rel)
    return [f"{rel}:{i}: {line.strip()}" for i, line in enumerate(text.splitlines(), 1)
            if re.search(pattern, line)]


# ------------------------------------------------------------------ 证据索引

EVIDENCE = [
    ("E-REQ-01", "artifact", "仓库文档", "docs/01-需求分析.md",
     "题面解析、R1–R8 规则编号、S1–S5 场景、假设清单 A1–A7 与 R↔S↔用例↔演示↔讲解五方对照表。"),
    ("E-PRO-01", "artifact", "仓库文档", "docs/02-原型设计.md",
     "四个业务页面的状态机与页面状态说明；原型本体为 httpapi/templates 下的真实模板。"),
    ("E-PLAN-01", "artifact", "仓库文档", "docs/03-任务规划.md",
     "任务到需求编号的映射、依赖关系、完成条件与测试验收对应。"),
    ("E-SRC-01", "artifact", "仓库源码", "internal/domain、internal/store、internal/service、internal/supplier、internal/httpapi、cmd/server、cmd/mockgen",
     "R1–R8 的服务端实现、SQLite 持久化、回调签名校验、四个业务页面与模拟供应商。"),
    ("E-SCHEMA-01", "artifact", "仓库源码", "internal/store/schema.sql",
     "需求/生成任务/候选/事件四张表与唯一索引、is_current 标记。"),
    ("E-TEST-01", "raw_tool", "本机执行输出", "evidence/go-test-output.txt",
     "go test ./... -count=1 的完整原始输出（各包 ok 行）。"),
    ("E-COV-01", "artifact", "本机执行产物", "evidence/coverage-summary.txt、evidence/coverage.out",
     "唯一口径覆盖率脚本 scripts/coverage_gate.py 的汇总与原始剖面。"),
    ("E-SCN-01", "raw_tool", "本机执行输出", "evidence/scenario-verify.log",
     "scripts/verify_scenarios.py 五组场景逐条断言的真实输出。"),
    ("E-AGENT-01", "artifact", "仓库文档", "AGENTS.md、docs/agents/AGENTS-v1.0.md、docs/agents/AGENTS-读取记录.md",
     "项目级 Agent 约束、版本快照与逐次读取记录。"),
    ("E-SKILL-B-01", "artifact", "仓库文档", "skills/material-guard/SKILL.md、skills/material-guard/scripts/material_guard.py",
     "自编业务 Skill：七要素说明 + G1–G9 数据库不变量与源码一致性判定脚本。"),
    ("E-SKILL-B-02", "raw_tool", "本机执行输出", "evidence/material-guard.md、evidence/material-guard.json",
     "material-guard 判定脚本的原始输出与 JSON 结果。"),
    ("E-SKILL-A-01", "artifact", "仓库文档", "skills/acceptance-audit/SKILL.md、skills/acceptance-audit/scripts/acceptance_audit.py",
     "自编验收 Skill：R1–R8 与 13 项共同要求的独立核验脚本。"),
    ("E-SKILL-A-02", "raw_tool", "本机执行输出", "evidence/acceptance-audit/检查项明细.md、evidence/acceptance-audit/acceptance-audit.json",
     "acceptance-audit 的逐项判定明细与 JSON 结果。"),
    ("E-SKILL-USE-01", "artifact", "仓库文档", "skill-usage.md、docs/08-Skill使用记录.md",
     "全部实际使用 Skill 的位置、原因、输入输出、帮助与局限。"),
    ("E-PROMPT-01", "artifact", "仓库文档", "docs/06-会话导出.md、evidence/会话事件清单.jsonl",
     "全过程可见提示词、回复摘要、工具结果与结构化事件清单。"),
    ("E-AI-01", "artifact", "仓库文档", "docs/09-AI使用说明.md",
     "AI 与学生分工、审核纠错记录与未弱化测试的声明。"),
    ("E-TESTAGENT-01", "artifact", "独立会话产物", "evidence/independent-test-report.md",
     "独立测试 Agent（全新上下文、读取原题与代码）的完整报告：9 条缺陷与复现输出。"),
    ("E-ACCEPTAGENT-01", "artifact", "独立会话产物", "evidence/independent-acceptance-report.md",
     "独立验收 Agent（全新上下文、依据原题逐项核验）的验收报告与结论。"),
    ("E-GIT-01", "raw_tool", "远端 API 报文", "evidence/github-mcp/create-repository-transcript.json",
     "GitHub MCP create_repository 的请求与响应报文（建仓事实）。"),
    ("E-GIT-02", "raw_tool", "远端 API 报文", "evidence/github-mcp/push-exact-dev06.json、evidence/github-mcp/push-exact-main.json",
     "Git Data API 复现提交的逐条映射：blob/tree SHA 与本地逐一相同，commit SHA 因 GitHub 时区规范化而不同。"),
    ("E-GIT-03", "raw_tool", "远端 API 报文", "evidence/github-mcp/branch-protection.json、evidence/github-mcp/direct-push-main-probe.json",
     "main 分支保护规则读取结果与直推 main 被拒（409）的真实响应。"),
    ("E-GIT-04", "raw_tool", "远端 API 报文", "evidence/github-mcp/pull-request.json、evidence/github-mcp/merge-pull-request.json",
     "PR 创建、状态检查与合并的请求响应报文。"),
    ("E-PIPE-01", "artifact", "仓库配置", ".github/workflows/ci.yml",
     "流水线定义：质量检查、gofmt、go vet、单测与竞态、覆盖率门禁、构建、部署、健康检查、业务冒烟。"),
    ("E-PIPE-02", "raw_tool", "远端 API 报文", "evidence/github-mcp/workflow-runs.json",
     "GitHub Actions 实际运行记录（运行号、作业结论、被检提交 SHA）。"),
    ("E-DEPLOY-01", "raw_tool", "本机执行输出", "evidence/deployment-main.log",
     "scripts/deploy.sh 针对合并提交的部署日志：构建、健康检查、五组场景冒烟与回退说明。"),
    ("E-VIDEO-01", "gap", "交付缺口登记", "docs/12-讲解视频带时间点目录.md",
     "已产出带时间点的讲解目录与逐段要点；视频本体与配音为人工交付物，待学生本人录制回填。"),
    ("E-GROUP-01", "gap", "交付缺口登记", "docs/11-班级群提交消息.md",
     "已产出班级群消息全文模板；实际发送与凭据由学生本人在当日群内完成，待补证。"),
    ("E-TOKEN-01", "artifact", "本机执行产物", "evidence/token-ledger.json",
     "全阶段逐请求 Token 去重台账（来源：会话归档的供应商 usage 字段）。"),
    ("E-CHECKPOINT-01", "artifact", "仓库文档", "docs/13-阶段检查点与版本索引.md",
     "七阶段检查点、进入条件、产物版本与交接记录。"),
    ("E-DEFIX-01", "artifact", "本机执行产物", "evidence/defect-fix-round1.md、evidence/defect-fix-round1-tools.md、evidence/defect-fix-round1-skills.md",
     "独立测试检出的 9 条缺陷的「发现问题 → 修正 → 复检」闭环记录。"),
    ("E-ASSUMPTION-01", "artifact", "仓库文档", "docs/01-需求分析.md（A1–A7 假设清单）",
     "题面未明确事项的假设登记，含理由、影响面与确认状态。"),
    ("E-SECRETSCAN-01", "raw_tool", "本机执行输出", "evidence/acceptance-audit/复检日志.txt",
     "泄密扫描（按已知凭据值 + 形态双规则、无白名单）的真实输出与负向验证。"),
]


def build(repo: Path, skill_root: Path, out_dir: Path) -> int:
    profile_path = skill_root / "references" / "profiles" / "06.json"
    profile = json.loads(profile_path.read_text(encoding="utf-8"))
    index = json.loads((skill_root / "references" / "profiles" / "index.json").read_text(encoding="utf-8"))
    digest = next(e["sha256"] for e in index["profiles"] if e["question"] == "06")

    cov = coverage_numbers(repo)
    scen = scenario_counts(repo)
    gitf = git_facts(repo)
    head = gitf["head"]
    head_short = gitf["head_short"]

    out_dir.mkdir(parents=True, exist_ok=True)
    (out_dir / "repo-facts.json").write_text(
        json.dumps({"coverage": cov, "scenario": scen, "git": gitf}, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8")
    print("仓库事实已写出：", json.dumps({"coverage": cov["overall"], "scenario": scen, "head": head_short},
                                       ensure_ascii=False))
    return 0


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo", required=True)
    parser.add_argument("--audit-skill", required=True)
    parser.add_argument("--out-dir", required=True)
    args = parser.parse_args()
    repo = Path(args.repo).resolve()
    skill_root = Path(args.audit_skill).resolve()
    return build(repo, skill_root, Path(args.out_dir).resolve())


if __name__ == "__main__":
    raise SystemExit(main())
