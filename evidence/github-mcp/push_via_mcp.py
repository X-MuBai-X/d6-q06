#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""通过 GitHub MCP 的 push_files 把本地交付内容推到远端分支，并归档「本地提交 ↔ 远端 SHA」映射。

背景：本机 git push over HTTPS 被阻断（Recv failure: Connection was reset），
按《反例与踩坑清单》D2 的固定顺序处置，改走 GitHub MCP 的 push_files 通道。

用法：
    python push_via_mcp.py --repo <本地仓库> --out evidence/github-mcp/push-mapping.json
"""
from __future__ import annotations

import argparse
import datetime
import json
import os
import pathlib
import subprocess
import sys

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
from mcp_call import MCPClient  # noqa: E402

OWNER = "X-MuBai-X"
NAME = "d6-q06"
MAX_CHUNK_BYTES = 110_000

# 推送批次：分支、提交消息、文件来源（本地相对路径）
BATCHES = [
    {
        "branch": "main",
        "message": "[docs] [v0.1.0] 初始化仓库说明与分支骨架",
        "paths": ["README.md", ".gitignore"],
    },
    {
        "branch": "dev/06",
        "message": "[docs] [v0.1.0] 补齐题面快照需求分析原型设计与任务规划",
        "paths": ["docs/_question-06.md", "docs/_common-requirements.md",
                  "docs/01-需求分析.md", "docs/02-原型设计.md", "docs/03-任务规划.md"],
    },
    {
        "branch": "dev/06",
        "message": "[docs] [v0.1.0] 落盘项目级AGENTS约束与版本快照读取记录",
        "paths": ["AGENTS.md", "docs/agents/AGENTS-v1.0.md", "docs/agents/AGENTS-读取记录.md"],
    },
    {
        "branch": "dev/06",
        "message": "[feat] [v0.1.0] [#TBD] 实现营销素材生图需求生成回调复核采用撤销完整闭环",
        "body": "工作项编号待核验：本机未安装 meegle CLI，无法执行 meegle workitem get 核验，"
                "按共同要求第 9 节保留 #TBD 占位并如实登记偏差。",
        "paths": ["go.mod", "go.sum"] + sorted(
            str(p.relative_to(".")).replace("\\", "/")
            for p in pathlib.Path("internal").rglob("*.go")) +
            sorted(str(p.relative_to(".")).replace("\\", "/")
                   for p in pathlib.Path("internal").rglob("*.sql")) +
            sorted(str(p.relative_to(".")).replace("\\", "/")
                   for p in pathlib.Path("internal").rglob("*.html")) +
            sorted(str(p.relative_to(".")).replace("\\", "/")
                   for p in pathlib.Path("cmd").rglob("*.go")),
    },
    {
        "branch": "dev/06",
        "message": "[opt] [v0.1.0] 实现material-guard与acceptance-audit判定脚本",
        "paths": sorted(str(p.relative_to(".")).replace("\\", "/")
                        for p in pathlib.Path("skills").rglob("*") if p.is_file()),
    },
    {
        "branch": "dev/06",
        "message": "[opt] [v0.1.0] 配置CI流水线覆盖率门禁与自动部署脚本",
        "paths": sorted(str(p.relative_to(".")).replace("\\", "/")
                        for p in pathlib.Path("scripts").rglob("*") if p.is_file()) +
                 [".github/workflows/ci.yml", "Dockerfile"],
    },
    {
        "branch": "dev/06",
        "message": "[docs] [v0.1.0] 补齐部署说明会话导出Skill记录AI使用说明与讲解目录",
        "paths": ["docs/04-部署与使用说明.md", "docs/06-会话导出.md", "docs/08-Skill使用记录.md",
                  "docs/09-AI使用说明.md", "docs/10-交付与发布说明.md", "docs/11-班级群提交消息.md",
                  "docs/12-讲解视频带时间点目录.md", "docs/14-分支实现链对照表.md",
                  "skill-usage.md"],
    },
    {
        "branch": "dev/06",
        "message": "[test] [v0.1.0] 归档单测覆盖率场景验证与验收证据",
        "paths": sorted(str(p.relative_to(".")).replace("\\", "/")
                        for p in pathlib.Path("evidence").rglob("*") if p.is_file() and p.name != "push-mapping.json"),
    },
]


def chunk_paths(repo: pathlib.Path, paths: list[str]) -> list[list[str]]:
    """按体积把文件列表切成若干批。"""
    chunks: list[list[str]] = []
    current: list[str] = []
    size = 0
    for rel in paths:
        target = repo / rel
        if not target.exists():
            continue
        length = target.stat().st_size
        if current and size + length > MAX_CHUNK_BYTES:
            chunks.append(current)
            current = []
            size = 0
        current.append(rel)
        size += length
    if current:
        chunks.append(current)
    return chunks


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo", required=True)
    parser.add_argument("--out", required=True)
    args = parser.parse_args()

    repo = pathlib.Path(args.repo).resolve()
    token = os.environ.get("GITHUB_PERSONAL_ACCESS_TOKEN", "").strip()
    if not token:
        raise SystemExit("缺少环境变量 GITHUB_PERSONAL_ACCESS_TOKEN")

    transcript: list = []
    client = MCPClient("https://api.githubcopilot.com/mcp/", token, transcript)
    client.post("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                               "clientInfo": {"name": "mcode-session", "version": "1.0"}})
    client.post("notifications/initialized", {}, notification=True)

    mapping = []
    for batch in BATCHES:
        chunks = chunk_paths(repo, batch["paths"])
        for index, chunk in enumerate(chunks):
            files = []
            for rel in chunk:
                content = (repo / rel).read_text(encoding="utf-8", errors="replace")
                files.append({"path": rel, "content": content})
            message = batch["message"]
            if len(chunks) > 1:
                message = f"{message}（{index + 1}/{len(chunks)}）"
            arguments = {"owner": OWNER, "repo": NAME, "branch": batch["branch"],
                         "message": message, "files": files}
            if batch.get("body"):
                arguments["body"] = batch["body"]
            result = client.post("tools/call", {"name": "push_files", "arguments": arguments})
            text = ""
            try:
                text = result["result"]["content"][0]["text"]
            except Exception:  # noqa: BLE001
                text = json.dumps(result, ensure_ascii=False)
            is_error = bool(result.get("result", {}).get("isError"))
            mapping.append({
                "branch": batch["branch"],
                "message": message,
                "file_count": len(files),
                "is_error": is_error,
                "response": text[:800],
            })
            print(f"[{'ERR' if is_error else 'OK '}] {batch['branch']} {message} ({len(files)} files)")
            if is_error:
                print(text[:400])

    # 记录远端分支
    branches = client.post("tools/call", {"name": "list_branches",
                                          "arguments": {"owner": OWNER, "repo": NAME}})
    payload = {
        "generated_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "repo": f"{OWNER}/{NAME}",
        "channel": "GitHub MCP push_files（git push over HTTPS 在本机被阻断，见 docs/10 偏差登记）",
        "batches": mapping,
        "branches_after_push": branches,
    }
    out = pathlib.Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")
    (out.parent / "push-transcript.json").write_text(json.dumps({
        "generated_at": payload["generated_at"],
        "note": "令牌未写入本文件；请求头 Authorization 已整体省略。",
        "transcript": transcript,
    }, ensure_ascii=False, indent=2), encoding="utf-8")
    print(f"映射与报文已写入 {out}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
