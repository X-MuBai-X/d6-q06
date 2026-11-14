#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""生成 student-project-audit v3.0 的两份输入。

  python evidence/audit/build_audit_input.py --repo <仓库根> --audit-skill <审计技能根> --out-dir evidence/audit

纪律：
* artifacts / checks 的固定字段直接复制技能内置 profiles/06.json，不增删改；
* 每条结论都落到真实文件/符号/日志；数字在运行时从仓库读取；
* 缺证记 unknown 并登记责任方，不用 NA、不用「通过」掩盖；
* 人工交付物（讲解视频、班级群提交）记 unknown / 待补证。
"""
from __future__ import annotations

import argparse
import hashlib
import json
import re
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import audit_content as C  # noqa: E402
import audit_tables as T  # noqa: E402

EVIDENCE = [
    ("E-REQ-01", "artifact", "仓库文档", "docs/01-需求分析.md",
     "题面解析、R1–R8 规则编号、S1–S5 场景、假设清单 A1–A7 与 R↔S↔用例↔演示↔讲解五方对照表。"),
    ("E-PRO-01", "artifact", "仓库文档", "docs/02-原型设计.md、internal/httpapi/templates/",
     "四个业务页面的状态机与页面状态说明；原型本体为真实模板。"),
    ("E-PLAN-01", "artifact", "仓库文档", "docs/03-任务规划.md",
     "任务到需求编号的映射、依赖关系、完成条件与测试验收对应。"),
    ("E-SRC-01", "artifact", "仓库源码", "internal/domain、internal/store、internal/service、internal/supplier、internal/httpapi、cmd/server、cmd/mockgen",
     "R1–R8 的服务端实现、SQLite 持久化、回调签名校验、四个业务页面与模拟供应商。"),
    ("E-SCHEMA-01", "artifact", "仓库源码", "internal/store/schema.sql",
     "需求/生成任务/候选/事件四张表、(requirement_id, request_no) 复合唯一索引与 is_current 标记。"),
    ("E-TEST-01", "raw_tool", "本机执行输出", "evidence/go-test-output.txt",
     "go test ./... -count=1 的完整原始输出（各包 ok 行）。"),
    ("E-COV-01", "artifact", "本机执行产物", "evidence/coverage-summary.txt、evidence/coverage.out",
     "唯一口径覆盖率脚本 scripts/coverage_gate.py 的汇总与原始剖面。"),
    ("E-SCN-01", "raw_tool", "本机执行输出", "evidence/scenario-verify.log、evidence/scenario-verify-default-clock.log",
     "scripts/verify_scenarios.py 五组场景逐条断言的真实输出（默认 60 秒口径与教学时钟两种运行）。"),
    ("E-AGENT-01", "artifact", "仓库文档", "AGENTS.md、docs/agents/AGENTS-v1.0.md、docs/agents/AGENTS-读取记录.md",
     "项目级 Agent 约束、版本快照与逐次读取记录。"),
    ("E-SKILL-B-01", "artifact", "仓库文档", "skills/material-guard/SKILL.md、skills/material-guard/scripts/material_guard.py",
     "自编业务 Skill：七要素说明 + G1–G9 数据库不变量与源码一致性判定脚本。"),
    ("E-SKILL-B-02", "raw_tool", "本机执行输出", "evidence/material-guard.md、evidence/material-guard.json",
     "material-guard 判定脚本的原始输出与 JSON 结果。"),
    ("E-SKILL-A-01", "artifact", "仓库文档", "skills/acceptance-audit/SKILL.md、skills/acceptance-audit/scripts/acceptance_audit.py",
     "自编验收 Skill：R1–R8 与共同要求逐项独立核验脚本（含泄密双规则与覆盖率复算）。"),
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
    ("E-PIPE-02", "raw_tool", "远端 API 报文", "evidence/github-mcp/workflow-runs.json、evidence/github-mcp/ci-run-43535651446-quality-gate.log",
     "GitHub Actions 实际运行记录与失败轮次的原始作业日志。"),
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
    ("E-SECRETSCAN-01", "raw_tool", "本机执行输出", "evidence/acceptance-audit/负向验证-泄密扫描.txt、evidence/material-guard-负向验证.txt",
     "泄密扫描（已知凭据值 + 敏感形态双规则、无白名单）与 material-guard 的负向验证原始输出。"),
    ("E-ASSUMPTION-01", "artifact", "仓库文档", "docs/01-需求分析.md（A1–A7 假设清单）",
     "题面未明确事项的假设登记，含理由、影响面与确认状态。"),
]


def git_facts(repo: Path) -> dict:
    def sh(*args: str) -> str:
        proc = subprocess.run(["git", "-C", str(repo), *args], capture_output=True, check=False)
        return proc.stdout.decode("utf-8", "replace")
    head = sh("rev-parse", "HEAD").strip()
    return {
        "head": head,
        "head_short": head[:7],
        "branch": sh("rev-parse", "--abbrev-ref", "HEAD").strip(),
        "log": [x for x in sh("log", "--format=%h|%s", "-30").splitlines() if x.strip()],
    }


def coverage_text(repo: Path) -> str:
    path = repo / "evidence" / "coverage-summary.txt"
    return path.read_text(encoding="utf-8", errors="replace") if path.exists() else ""


def coverage_numbers(repo: Path) -> dict:
    text = coverage_text(repo)
    out = {"overall": None, "block": None, "packages": {}}
    m = re.search(r"整体覆盖率】\s*语句\s*([0-9.]+)%\s*块\s*([0-9.]+)%", text)
    if m:
        out["overall"], out["block"] = m.group(1), m.group(2)
    for pkg, stmt, blk in re.findall(r"((?:internal|cmd)/[a-z]+)\s+语句\s+([0-9.]+)%\s+块\s+([0-9.]+)%", text):
        out["packages"][pkg] = {"statement": stmt, "block": blk}
    return out


def token_ledger(repo: Path) -> dict:
    path = repo / "evidence" / "token-ledger.json"
    if not path.exists():
        return {}
    return json.loads(path.read_text(encoding="utf-8"))


# 问题登记：每条 partial/fail 或「通过但有残余不足」的计分项都关联到唯一问题
ISSUES = [
    {
        "id": "ISS-01",
        "root_cause": "本机未安装 meegle CLI，工作项编号无法通过官方 CLI 核验",
        "fact": "feat/fix 提交的工作项编号保留 #TBD 占位，未经 meegle workitem get 核验",
        "source": "题面共同要求第 9 节：编号未经有效核验的提交不计为合规提交",
        "impact": "B4-03 提交消息格式合规但编号未核验，提交与工作项无法双向追溯",
        "fix": "在具备 meegle CLI 的环境执行 meegle workitem get --work-item-id <编号> --project-key r17te9，核验通过后改写提交消息并登记核验记录",
        "retest": "重跑 acceptance-audit 的 C-10 项并附核验日志",
        "history": "dev/06 上 4 条 feat/fix 提交均保留占位，未伪造编号",
        "deduction_basis": "状态轴：编号未核验导致该项只能记 partial",
        "deduplication": "与 ISS-02 的质量根因不同：ISS-01 是「无法核验」的环境缺口，ISS-02 是「占位导致可追溯性下降」的流程影响",
        "certainty": "confirmed",
        "owner": "platform",
        "evidence": ["E-GIT-02"],
        "deductions": [
            {"check_id": "06-B4-03", "axis": "status", "independent_impact": "编号未核验，提交不算完整合规提交"},
        ],
    },
    {
        "id": "ISS-02",
        "root_cause": "提交消息中的工作项编号为占位符，与真实工作项缺少可核验的关联",
        "fact": "提交消息格式 [类型] [版本号] [编号] 中文描述 合规，但编号位是 #TBD",
        "source": "题面共同要求第 9 节提交消息规范",
        "impact": "评审者无法从提交反查工作项，影响 B4-03 的提交可追溯质量",
        "fix": "核验通过后改写 feat/fix 提交消息，并在 docs/10 登记核验记录",
        "retest": "核验后复查 git log 消息格式",
        "history": "初次记录；编号核验通过后按新周期复验，并改写 feat/fix 提交消息后关闭。",
        "deduction_basis": "质量轴：可追溯链不完整",
        "deduplication": "不重复 ISS-01 的状态缺口，只扣质量",
        "certainty": "confirmed",
        "owner": "student",
        "evidence": ["E-GIT-02"],
        "deductions": [
            {"check_id": "06-B4-03", "axis": "quality", "independent_impact": "提交与工作项缺少可核验关联"},
        ],
    },
    {
        "id": "ISS-03",
        "root_cause": "本机缺少 C 编译器且 Docker 守护进程未运行，go test -race 无法执行",
        "fact": "并发唯一性与幂等只有功能级验证，竞态检测结论待补证",
        "source": "独立测试报告 §4 未覆盖范围；CI 中已配置 go test -race 步骤",
        "impact": "A3-06 的「适用并发/竞争检测」只完成功能级验证",
        "fix": "以 CI 的 -race 实际运行记录补证",
        "retest": "CI 流水线运行成功后核对运行记录中的 -race 步骤结论",
        "history": "初次记录；CI 流水线给出 go test -race 实际运行结论后复验并关闭。",
        "deduction_basis": "质量轴：缺少竞态检测的直接证据",
        "deduplication": "与 ISS-01 无关，属环境能力缺口",
        "certainty": "confirmed",
        "owner": "platform",
        "evidence": ["E-TESTAGENT-01", "E-PIPE-02"],
        "deductions": [
            {"check_id": "06-A3-06", "axis": "quality", "independent_impact": "竞态检测无本机证据，质量不足 excellent"},
        ],
    },
    {
        "id": "ISS-04",
        "root_cause": "本机无云主机，部署在流水线运行器与本机进程内完成，无公网常驻地址",
        "fact": "部署与冒烟成功，但上线地址不是可长期访问的公网 URL",
        "source": "evidence/deployment-main.log 的目标地址与回退说明",
        "impact": "A4-06 的「上线地址可访问」只在本机/运行器内成立",
        "fix": "配置 DEPLOY_TARGET 与 DEPLOY_TOKEN 后由流水线重新发布到常驻环境",
        "retest": "重新部署后从外部访问该地址并复跑冒烟",
        "history": "初次记录；接入常驻验收环境并复跑冒烟后复验并关闭。",
        "deduction_basis": "质量轴：部署形态受限",
        "deduplication": "与人工交付物缺口（视频、群提交）无关",
        "certainty": "confirmed",
        "owner": "student",
        "evidence": ["E-DEPLOY-01"],
        "deductions": [
            {"check_id": "06-A4-06", "axis": "quality", "independent_impact": "上线地址无公网常驻形态，质量不足 excellent"},
        ],
    },
    {
        "id": "ISS-05",
        "root_cause": "GitHub 会把提交的时区规范化为 +0000，与本地的 +0800 偏移不同",
        "fact": "同一份代码在远端的 tree SHA 与本地逐一相同，但 commit SHA 不同",
        "source": "evidence/github-mcp/push-exact-dev06.json 的逐条映射",
        "impact": "源提交与远端提交无法按 SHA 直接相等比对，只能按 tree SHA 关联",
        "fix": "以 tree SHA 映射表作为关联依据并在文档中固定说明；若需 SHA 完全一致，可统一以 UTC 偏移重建提交历史",
        "retest": "复跑推送脚本核对映射表中 tree_equal 全为 true",
        "history": "初次记录；每次推送均归档 tree SHA 映射表，核对无偏差后关闭。",
        "deduction_basis": "质量轴：版本关联需借助映射而非 SHA 相等",
        "deduplication": "与 ISS-01 的编号核验问题不同",
        "certainty": "confirmed",
        "owner": "platform",
        "evidence": ["E-GIT-02"],
        "deductions": [
            {"check_id": "06-B4-05", "axis": "quality", "independent_impact": "版本关联依赖 tree SHA 映射，质量不足 excellent"},
        ],
    },
    {
        "id": "ISS-06",
        "root_cause": "会话事件清单以结构化摘要为主，未逐条粘贴全部可见回复原文",
        "fact": "evidence/会话事件清单.jsonl 记录了提示、动作、结果与失败，但回复原文以摘要形式保存",
        "source": "evidence/会话事件清单.jsonl、docs/06-会话导出.md",
        "impact": "B2-03 的「全量会话、回复、工具结果」只能按摘要级追溯",
        "fix": "如需逐字原文，可另行导出原始会话快照（本仓库保留结构化底账 + 快照目录指引）",
        "retest": "抽查 5 条事件能从提示追到实现与验证",
        "history": "初次记录；补齐逐字原文导出后抽查追溯并关闭。",
        "deduction_basis": "质量轴：追溯粒度为摘要级",
        "deduplication": "与 ISS-02 的编号问题不同",
        "certainty": "confirmed",
        "owner": "student",
        "evidence": ["E-PROMPT-01"],
        "deductions": [
            {"check_id": "06-B2-03", "axis": "quality", "independent_impact": "回复以摘要保存，追溯粒度受限"},
            {"check_id": "06-B2-04", "axis": "quality", "independent_impact": "分支提示链在摘要粒度下可追溯性略低"},
            {"check_id": "06-B2-05", "axis": "quality", "independent_impact": "部分提示的上下文以要点形式留存"},
        ],
    },
    {
        "id": "ISS-07",
        "root_cause": "缺陷修复后以功能级回归与场景脚本复检为主，边界组合未穷举",
        "fact": "9 条缺陷均有对应回归用例，但未对每个修复点做属性测试或穷举组合",
        "source": "evidence/defect-fix-round1*.md 的复检清单",
        "impact": "A1-07/A1-12/A1-13/A2-02/A2-03/A2-04 等项的残余风险高于 excellent",
        "fix": "如需更高置信度，可补充状态机的属性测试（随机状态迁移序列）",
        "retest": "补充属性测试后重跑覆盖率门禁与场景脚本",
        "history": "初次记录；补齐状态机属性测试后重跑门禁与场景脚本并关闭。",
        "deduction_basis": "质量轴：回归充分但未穷举",
        "deduplication": "与 ISS-03 的环境缺口无关",
        "certainty": "confirmed",
        "owner": "student",
        "evidence": ["E-DEFIX-01", "E-TEST-01"],
        "deductions": [
            {"check_id": "06-A1-07", "axis": "quality", "independent_impact": "撤销不变量仅功能级验证"},
            {"check_id": "06-A1-12", "axis": "quality", "independent_impact": "巡检分支未穷举时间序列"},
            {"check_id": "06-A1-13", "axis": "quality", "independent_impact": "并发组合未穷举"},
            {"check_id": "06-A2-02", "axis": "quality", "independent_impact": "状态迁移未做属性测试"},
            {"check_id": "06-A2-03", "axis": "quality", "independent_impact": "资源守恒边界（次数上限、时间窗、候选数量）仅功能级验证，未穷举边界组合"},
            {"check_id": "06-A2-04", "axis": "quality", "independent_impact": "幂等与并发未做属性测试"},
        ],
    },
    {
        "id": "ISS-08",
        "root_cause": "流水线在本机无法完整复现，部署作业依赖运行器内的验收环境",
        "fact": "CI 的部署作业在运行器内启动进程并完成冒烟，未指向公网环境",
        "source": ".github/workflows/ci.yml 的 deploy 作业与 DEPLOY_TARGET 变量",
        "impact": "A4-05/A4-07 的流水线与回退验证依赖运行器环境",
        "fix": "配置常驻验收环境后重跑流水线并复验回退步骤",
        "retest": "在常驻环境重跑流水线并核对回退命令",
        "history": "初次记录；在常驻环境重跑流水线并实测回退后复验并关闭。",
        "deduction_basis": "质量轴：部署形态受限",
        "deduplication": "与 ISS-04 相关但归因不同：ISS-04 是地址形态，ISS-08 是流水线可在性验证范围",
        "certainty": "confirmed",
        "owner": "student",
        "evidence": ["E-PIPE-01", "E-PIPE-02", "E-DEPLOY-01"],
        "deductions": [
            {"check_id": "06-A4-05", "axis": "quality", "independent_impact": "流水线部署作业仅在运行器内验证"},
            {"check_id": "06-A4-07", "axis": "quality", "independent_impact": "回退方法未在常驻环境实测"},
        ],
    },
    {
        "id": "ISS-09",
        "root_cause": "阶段检查点以文档与提交历史形式记录，未引入独立的检查点工具链",
        "fact": "docs/13 记录七阶段进入条件与产物，依赖人工维护",
        "source": "docs/13-阶段检查点与版本索引.md",
        "impact": "B1-02/B1-03 的检查点质量依赖人工维护的准确性",
        "fix": "如需更强约束，可把阶段门禁写成可执行脚本并纳入流水线",
        "retest": "补充门禁脚本后重跑流水线核对阶段顺序",
        "history": "初次记录；阶段门禁脚本化并纳入流水线后复验并关闭。",
        "deduction_basis": "质量轴：检查点依赖人工维护",
        "deduplication": "与 ISS-08 的流水线部署形态无关",
        "certainty": "confirmed",
        "owner": "student",
        "evidence": ["E-CHECKPOINT-01"],
        "deductions": [
            {"check_id": "06-B1-02", "axis": "quality", "independent_impact": "阶段门禁主要靠人工核对"},
            {"check_id": "06-B1-03", "axis": "quality", "independent_impact": "检查点索引依赖人工维护"},
        ],
    },
    {
        "id": "ISS-10",
        "root_cause": "Skill 判定脚本基于静态特征与数据库不变量，无法覆盖全部语义",
        "fact": "acceptance-audit 的 R2-01 等项仍是源码特征判定，material-guard 只覆盖已建模的不变量",
        "source": "skills/acceptance-audit/scripts/acceptance_audit.py、skills/material-guard/scripts/material_guard.py",
        "impact": "B3-01/B3-02/B3-03 的判定覆盖面有限，仍需人工与独立 Agent 复核",
        "fix": "继续把新发现的语义缺口转成脚本判据（已对 R2-01、泄密扫描、覆盖率复算做过三轮增强）",
        "retest": "每轮增强后重跑两个 Skill 并保留复检输出",
        "history": "初次记录；每轮判据增强后重跑两个 Skill 并保留复检输出后关闭。",
        "deduction_basis": "质量轴：判定覆盖面有限",
        "deduplication": "与 ISS-06 的会话追溯粒度不同",
        "certainty": "confirmed",
        "owner": "student",
        "evidence": ["E-SKILL-A-01", "E-SKILL-B-01", "E-SKILL-USE-01"],
        "deductions": [
            {"check_id": "06-B3-01", "axis": "quality", "independent_impact": "业务 Skill 判定覆盖有限"},
            {"check_id": "06-B3-02", "axis": "quality", "independent_impact": "验收 Skill 判定覆盖有限"},
            {"check_id": "06-B3-03", "axis": "quality", "independent_impact": "Skill 总结中的局限说明需随判据演进更新"},
        ],
    },
]


def build(repo: Path, skill_root: Path, out_dir: Path) -> int:
    profile_path = skill_root / "references" / "profiles" / "06.json"
    profile = json.loads(profile_path.read_text(encoding="utf-8"))
    index = json.loads((skill_root / "references" / "profiles" / "index.json").read_text(encoding="utf-8"))
    digest = next(e["sha256"] for e in index["profiles"] if e["question"] == "06")

    gitf = git_facts(repo)
    cov = coverage_numbers(repo)
    ledger = token_ledger(repo)
    rev = gitf["head"]
    rev_short = gitf["head_short"]

    # ---------- artifacts ----------
    artifacts = []
    for item in profile["artifacts"]:
        key = item["id"]
        presence, validity, location, reason, owner, evidence, scenario, usage, quality, impact, fix = C.ARTIFACTS[key]
        artifacts.append({
            "id": key, "presence": presence, "validity": validity, "location": location,
            "reason": reason, "owner": owner, "evidence": evidence, "scenario": scenario,
            "usage": usage, "quality_summary": quality, "result_impact": impact, "fix": fix,
        })

    # ---------- checks ----------
    checks = []
    issue_by_check = {}
    for issue in ISSUES:
        for deduction in issue["deductions"]:
            issue_by_check.setdefault(deduction["check_id"], []).append(issue["id"])
    for spec in profile["requirements"]:
        key = spec["id"]
        status, quality, basis, reason, owner, evidence, fix = C.CHECKS[key]
        item = {field: spec[field] for field in spec}
        item.update({
            "status": status, "quality": quality, "quality_basis": basis, "reason": reason,
            "owner": owner, "evidence": evidence, "fix": fix,
            "issue_ids": issue_by_check.get(key, []),
        })
        checks.append(item)

    # ---------- audit_meta ----------
    audit_meta = {
        "audit_id": "D6-Q06-SELF-2026-11-14-01",
        "auditor": "交付执行方自检（student-project-audit v3.0 官方计分器）",
        "audit_date": "2026-11-14T09:00:00+08:00",
        "environment": "Windows 10.0.26100 / PowerShell 5.1 / go1.27.1 windows-amd64 / Python 3.14.8；CI 侧为 ubuntu-latest + go1.27.4",
        "tool_versions": "student-project-audit rubric 3.0；score_audit.py 与 render_report.py 取自技能内置 scripts/",
        "rubric_published_at": "2026-11-13（本次交付取得的工具包内 references/ 随题面一并提供）",
        "publication_evidence": "skill-usage.md、docs/09-AI使用说明.md 记录了清单与计分规则的取得方式与使用范围",
        "profile_approved_by": "未完成：本次交付未获得教师对 profiles/06.json 的书面核准记录",
        "profile_release_evidence": "未完成：无清单发布/核准留档，按合同登记为缺口并归责 teacher",
        "batch": "2组-题目06",
        "calibration_id": "未完成：本次交付未获得评分校准记录",
        "calibration_evidence": "未完成：无校准记录，按合同登记为缺口并归责 teacher",
        "review_policy": "发现即修正并留「发现问题→修正→复检」闭环记录；证据缺失记待补证，不以 NA 或通过代替",
        "supplement_deadline": "补证项随每日群提交同步补充，直至教师收件",
        "resubmission_policy": "允许在补证后重跑官方计分器并保留旧报告，不静默重算",
        "appeal_channel": "班级群与教师评阅渠道",
        "readiness": {
            "rules_published": True,
            "profile_approved": False,
            "calibrated": False,
            "evidence": ["E-CHECKPOINT-01", "E-SKILL-USE-01"],
        },
        "mode": "initial",
        "disputed": False,
        "second_review": {"completed": False},
    }

    # ---------- token ----------
    requests = ledger.get("requests", [])
    # 台账原始 evidence 是「取证位置描述」，不是报告证据编号；
    # 报告契约要求每条证据引用都能解析到已登记证据，故统一挂到 E-TOKEN-01，
    # 原始取证位置保留在 source_pointer 中，不丢失可追溯性。
    for row in requests:
        pointer = row.get("evidence")
        row["source_pointer"] = pointer
        row["evidence"] = ["E-TOKEN-01"]
    token_usage = {
        "verified": bool(requests),
        "complete": bool(requests),
        "baseline_comparable": False,
        "total_tokens": ledger.get("total_tokens"),
        "baseline_tokens": None,
        "owner": "teacher",
        "reason": "全阶段逐请求用量已按供应商 usage 字段去重统计（口径见 evidence/token-ledger.json）；"
                  "但教师尚未发布同题同范围的可信基准 B，故不做消耗比与额外浪费扣分计算，Token 项暂记未知",
        "evidence": ["E-TOKEN-01"],
        "requests": requests,
        "waste": [],
    }

    audit_input = {
        "schema_version": "3.0",
        "student": "xishiyao",
        "question": "06",
        "revision": f"dev/06@{rev_short}（完整 SHA {rev}）",
        "profile_sha256": digest,
        "audit_meta": audit_meta,
        "artifacts": artifacts,
        "checks": checks,
        "issues": ISSUES,
        "token_usage": token_usage,
    }

    # ---------- report-data ----------
    completion = []
    for req_id, row in T.COMPLETION.items():
        role, ops, expect, actual, status, revision, evidence = row
        completion.append({
            "requirement_id": req_id, "role_precondition": role, "operations": ops,
            "expected": expect, "actual": actual, "status": status, "revision": revision,
            "evidence": evidence,
        })

    prompts = []
    for row in T.PROMPTS:
        (pid, req_ids, task, preds, time, location, context, action, tool_result, judgment,
         revision, verification, status, gap, approach, evidence) = row
        prompts.append({
            "id": pid, "requirement_ids": req_ids, "task": task, "predecessors": preds,
            "time": time, "prompt_location": location, "context": context, "ai_action": action,
            "tool_result": tool_result, "student_judgment": judgment, "revision": revision,
            "verification": verification, "status": status, "gap": gap, "correct_approach": approach,
            "evidence": evidence,
        })

    workflow = []
    for stage, preds, time, snapshot, handoff, status, gate, evidence in T.WORKFLOW:
        workflow.append({
            "stage": stage, "predecessors": preds, "time": time, "snapshot": snapshot,
            "handoff": handoff, "status": status, "gate": gate, "evidence": evidence,
        })

    code_runtime = []
    for metric, status, command, expect, actual, exit_code, env, revision, evidence in T.CODE_RUNTIME:
        code_runtime.append({
            "metric": metric, "status": status, "command": command, "expected": expect,
            "actual": actual, "exit_code": exit_code, "environment": env, "revision": revision,
            "evidence": evidence,
        })

    agents_skills = []
    for (aid, kind, name, version, location, scenario, loaded_at, usage_location, reason,
         io_text, help_text, limitations, status, evidence) in T.AGENTS_SKILLS:
        agents_skills.append({
            "id": aid, "kind": kind, "name": name, "source_version": version, "location": location,
            "scenario": scenario, "loaded_at": loaded_at, "usage_location": usage_location,
            "reason": reason, "input_output": io_text, "help": help_text, "limitations": limitations,
            "status": status, "evidence": evidence,
        })

    limitations = []
    for lid, scope, owner, reason, effect, nxt, evidence in T.LIMITATIONS:
        limitations.append({
            "id": lid, "scope": scope, "owner": owner, "reason": reason,
            "score_effect": effect, "next_action": nxt, "evidence": evidence,
        })

    evidence_rows = []
    for eid, level, provenance, location, summary in EVIDENCE:
        evidence_rows.append({
            "id": eid, "level": level, "provenance": provenance, "location": location,
            "summary": summary, "revision_time": rev_short, "scope": "题目 06 交付物",
        })

    report_data = {
        "schema_version": "3.0",
        "meta": {
            "delivery_date": "2026-11-14",
            "deployment": "scripts/deploy.sh 针对合并提交执行构建、健康检查与五组场景冒烟；"
                          "上线地址为流水线运行器/本机进程内的验收环境，无公网常驻主机（见 limitations）",
            "version_chain": f"main 初始化 2e79dd4 ← dev/06 业务提交链（本地 {rev_short}，远端同 tree） ← PR 合并提交",
            "scope": "题目 06 营销素材 AI 生图、重做与确认交付的全部交付物、证据与流程",
            "token_note": f"统计范围：需求准备至最终交付的全部 AI 调用（含三个子 Agent 会话）；"
                          f"口径：会话归档中供应商 usage.totalTokens，按 (provider, request_id) 去重；"
                          f"合计 {ledger.get('total_tokens')} tokens，请求 {len(requests)} 条；"
                          f"阶段小计见 evidence/token-ledger.json 的 by_stage；"
                          f"基准：教师未发布同题同范围基准（profiles/06.json token_baselines 为空），"
                          f"故不做消耗比与额外浪费扣分计算，Token 项记未知，责任方 teacher",
        },
        "used_skill_ids": ["AS-MG", "AS-AA"],
        "tables": {
            "completion": completion,
            "code_runtime": code_runtime,
            "workflow": workflow,
            "prompts": prompts,
            "agents_skills": agents_skills,
            "limitations": limitations,
            "understanding": [],
            "daily": [],
        },
        "evidence": evidence_rows,
    }

    out_dir.mkdir(parents=True, exist_ok=True)
    (out_dir / "audit-input.json").write_text(
        json.dumps(audit_input, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    (out_dir / "report-data.json").write_text(
        json.dumps(report_data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"已生成 {out_dir/'audit-input.json'} 与 {out_dir/'report-data.json'}")
    print("覆盖率摘要：", json.dumps(cov, ensure_ascii=False))
    return 0


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo", required=True)
    parser.add_argument("--audit-skill", required=True)
    parser.add_argument("--out-dir", required=True)
    args = parser.parse_args()
    return build(Path(args.repo).resolve(), Path(args.audit_skill).resolve(), Path(args.out_dir).resolve())


if __name__ == "__main__":
    raise SystemExit(main())
