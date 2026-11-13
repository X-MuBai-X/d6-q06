#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""远端仓库运维：种子提交 / 分支保护 / 建 PR / 合并 / 直推受拒取证。

说明
----
1. GitHub 对**空仓库**禁用全部 Git Data API（git/blobs、git/trees、git/commits 均返回
   409 "Git Repository is empty"）。因此先用 Contents API 建一个占位提交把仓库「点亮」，
   随后用 push_exact_commits.py 复现本地真实提交，并把分支引用强制定位到本地提交。
   该占位提交不在任何分支历史中，此处如实记录，不隐藏。
2. 分支保护、PR、合并、直推受拒等均通过 REST 调用，返回体与状态码**原样落盘**。
3. 令牌只从环境变量 GITHUB_PERSONAL_ACCESS_TOKEN 读取。

用法：
  python repo_ops.py seed      --owner X --repo d6-q06 --out evidence/github-mcp/xxx.json
  python repo_ops.py protect   --owner X --repo d6-q06 --branch main --out ...
  python repo_ops.py direct-push-probe --owner X --repo d6-q06 --branch main --message "..." --out ...
  python repo_ops.py pr        --owner X --repo d6-q06 --head dev/06 --base main --title "..." --body-file f --out ...
  python repo_ops.py checks    --owner X --repo d6-q06 --pr N --out ...
  python repo_ops.py merge     --owner X --repo d6-q06 --pr N --out ...
  python repo_ops.py status    --owner X --repo d6-q06 --out ...
"""
from __future__ import annotations

import argparse
import base64
import json
import os
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

API = "https://api.github.com"


def call(method: str, path: str, payload=None, token: str = None):
    token = token or os.environ["GITHUB_PERSONAL_ACCESS_TOKEN"]
    data = json.dumps(payload).encode("utf-8") if payload is not None else None
    headers = {
        "Authorization": f"Bearer {token}",
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
        "User-Agent": "creative-studio-repo-ops",
    }
    if data is not None:
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(API + path, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            body = resp.read().decode("utf-8", "replace")
            return resp.status, (json.loads(body) if body.strip() else {})
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", "replace")
        try:
            return exc.code, json.loads(body)
        except json.JSONDecodeError:
            return exc.code, {"raw": body}


def dump(path: str, payload: dict) -> None:
    out = Path(path)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"已写入 {out}")


def cmd_seed(args) -> int:
    base = f"/repos/{args.owner}/{args.repo}"
    status, repo = call("GET", base)
    status, branches = call("GET", base + "/branches")
    has_branch = bool(branches)
    payload = {"seeded": False, "reason": ""}
    if has_branch:
        payload["reason"] = "仓库已有分支，无需占位提交"
    else:
        content = base64.b64encode(b"placeholder\n").decode("ascii")
        body = {
            "message": "chore: 仓库占位提交（GitHub 对空仓库禁用 Git Data API 的绕行产物，非业务提交）",
            "content": content,
            "branch": "main",
        }
        st, resp = call("PUT", base + "/contents/.seed-placeholder", body)
        payload.update({"seeded": True, "status": st, "response": resp})
        if st not in (200, 201):
            print(f"占位提交失败: {st} {resp}", file=sys.stderr)
            return 3
    dump(args.out, payload)
    return 0


def cmd_protect(args) -> int:
    base = f"/repos/{args.owner}/{args.repo}/branches/{args.branch}/protection"
    body = {
        "required_status_checks": {
            "strict": True,
            "contexts": [args.context],
        },
        "enforce_admins": True,
        "required_pull_request_reviews": None,
        "restrictions": None,
        "required_linear_history": False,
        "allow_force_pushes": False,
        "allow_deletions": False,
        "required_conversation_resolution": False,
    }
    st, resp = call("PUT", base, body)
    # 读取一次保护规则作为证据
    st2, current = call("GET", base)
    dump(args.out, {"put_status": st, "put_response": resp, "get_status": st2, "protection": current})
    return 0 if st in (200, 201) else 3


def cmd_direct_push_probe(args) -> int:
    """在保护分支上尝试直接提交内容，期望被拒（403/409/422），记录真实状态码。"""
    base = f"/repos/{args.owner}/{args.repo}"
    content = base64.b64encode(("直推受拒绝证 " + time.strftime("%Y-%m-%d %H:%M:%S") + "\n").encode()).decode()
    body = {
        "message": args.message,
        "content": content,
        "branch": args.branch,
        "path": ".direct-push-probe",
    }
    st, resp = call("PUT", base + "/contents/.direct-push-probe", body)
    dump(args.out, {"attempt": "PUT contents on protected branch", "branch": args.branch,
                    "status": st, "response": resp})
    print(f"直推 {args.branch} 状态码 {st}")
    return 0


def cmd_pr(args) -> int:
    base = f"/repos/{args.owner}/{args.repo}"
    body = {
        "title": args.title,
        "head": args.head,
        "base": args.base,
        "body": Path(args.body_file).read_text(encoding="utf-8"),
        "maintainer_can_modify": True,
    }
    st, resp = call("POST", base + "/pulls", body)
    dump(args.out, {"status": st, "response": resp})
    return 0 if st in (200, 201) else 3


def cmd_checks(args) -> int:
    base = f"/repos/{args.owner}/{args.repo}"
    out = {"checks": [], "runs": []}
    st, resp = call("GET", f"{base}/commits/{args.sha}/check-runs")
    out["check_runs_status"] = st
    out["checks"] = [
        {"name": c.get("name"), "status": c.get("status"), "conclusion": c.get("conclusion"),
         "html_url": c.get("html_url"), "started_at": c.get("started_at"),
         "completed_at": c.get("completed_at")}
        for c in (resp.get("check_runs") or [])
    ]
    st2, runs = call("GET", f"{base}/actions/runs?head_sha={args.sha}")
    out["workflow_runs_status"] = st2
    out["runs"] = [
        {"id": r.get("id"), "name": r.get("name"), "status": r.get("status"),
         "conclusion": r.get("conclusion"), "html_url": r.get("html_url"),
         "head_sha": r.get("head_sha"), "event": r.get("event"),
         "created_at": r.get("created_at"), "run_number": r.get("run_number")}
        for r in (runs.get("workflow_runs") or [])
    ]
    dump(args.out, out)
    return 0


def cmd_merge(args) -> int:
    base = f"/repos/{args.owner}/{args.repo}"
    body = {"merge_method": args.method, "commit_title": args.commit_title,
            "commit_message": args.commit_message}
    st, resp = call("PUT", f"{base}/pulls/{args.pr}/merge", body)
    dump(args.out, {"status": st, "response": resp})
    print(f"合并状态码 {st}")
    return 0 if st == 200 else 3


def cmd_status(args) -> int:
    base = f"/repos/{args.owner}/{args.repo}"
    out = {}
    for key, path in (("repo", base), ("branches", base + "/branches"),
                      ("protection", f"{base}/branches/{args.branch}/protection"),
                      ("pulls", base + "/pulls?state=all")):
        st, resp = call("GET", path)
        out[key] = {"status": st, "response": resp}
    dump(args.out, out)
    return 0


def main() -> int:
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest="cmd", required=True)
    for name in ("seed", "protect", "direct-push-probe", "pr", "checks", "merge", "status"):
        p = sub.add_parser(name)
        p.add_argument("--owner", required=True)
        p.add_argument("--repo", required=True)
        p.add_argument("--out", required=True)
        if name == "protect":
            p.add_argument("--branch", default="main")
            p.add_argument("--context", default="quality-gate")
        if name == "direct-push-probe":
            p.add_argument("--branch", default="main")
            p.add_argument("--message", default="chore: 直推受拒绝证")
        if name == "pr":
            p.add_argument("--head", required=True)
            p.add_argument("--base", default="main")
            p.add_argument("--title", required=True)
            p.add_argument("--body-file", required=True)
        if name == "checks":
            p.add_argument("--sha", required=True)
        if name == "merge":
            p.add_argument("--pr", required=True)
            p.add_argument("--method", default="merge")
            p.add_argument("--commit-title", default="")
            p.add_argument("--commit-message", default="")
        if name == "status":
            p.add_argument("--branch", default="main")
    args = parser.parse_args()
    handlers = {"seed": cmd_seed, "protect": cmd_protect, "direct-push-probe": cmd_direct_push_probe,
                "pr": cmd_pr, "checks": cmd_checks, "merge": cmd_merge, "status": cmd_status}
    return handlers[args.cmd](args)


if __name__ == "__main__":
    sys.exit(main())
