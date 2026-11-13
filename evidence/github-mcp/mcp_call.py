#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""GitHub MCP Server 通用调用器（streamable-http JSON-RPC）。

用法：
    python mcp_call.py --tool create_repository --args '{"name":"d6-q06","owner_type":"user","private":true}' --transcript out.json
    python mcp_call.py --tool create_branch --args '{"owner":"X-MuBai-X","repo":"d6-q06","branch":"dev/06"}' --transcript out.json
    python mcp_call.py --list

说明：
    - 只走 MCP 协议（initialize → notifications/initialized → tools/call），
      不直接调用 GitHub REST 建仓/建分支/建 PR 接口；
    - 令牌只从环境变量 GITHUB_PERSONAL_ACCESS_TOKEN 读取，不写盘、不进仓库；
    - 完整请求与响应写入 transcript，作为「MCP 建仓/建分支/PR」证据。
"""
from __future__ import annotations

import argparse
import datetime
import json
import os
import pathlib
import urllib.error
import urllib.request

ENDPOINT = "https://api.githubcopilot.com/mcp/"
PROTOCOL_VERSION = "2025-06-18"


class MCPClient:
    def __init__(self, endpoint: str, token: str, transcript: list) -> None:
        self.endpoint = endpoint
        self.token = token
        self.transcript = transcript
        self.session_id = ""
        self._id = 0

    def _next_id(self) -> int:
        self._id += 1
        return self._id

    def post(self, method: str, params: dict | None = None, notification: bool = False):
        payload = {"jsonrpc": "2.0", "method": method}
        if not notification:
            payload["id"] = self._next_id()
        if params is not None:
            payload["params"] = params
        body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        headers = {
            "Authorization": "Bearer " + self.token,
            "Content-Type": "application/json",
            "Accept": "application/json, text/event-stream",
        }
        if self.session_id:
            headers["Mcp-Session-Id"] = self.session_id
        request = urllib.request.Request(self.endpoint, data=body, headers=headers, method="POST")
        try:
            with urllib.request.urlopen(request, timeout=90) as response:
                raw = response.read().decode("utf-8", errors="replace")
                if not self.session_id:
                    self.session_id = response.headers.get("Mcp-Session-Id", "")
        except urllib.error.HTTPError as exc:
            raw = exc.read().decode("utf-8", errors="replace")
            self.transcript.append({"request": payload, "status": exc.code, "response": raw})
            raise SystemExit(f"MCP 请求失败（HTTP {exc.code}）：{raw[:800]}")
        self.transcript.append({"request": payload, "status": 200, "response": raw})
        if notification:
            return None
        return self._parse(raw)

    @staticmethod
    def _parse(raw: str):
        raw = raw.strip()
        if raw.startswith("event:") or raw.startswith("data:"):
            for line in raw.splitlines():
                if line.startswith("data:"):
                    return json.loads(line[5:].strip())
            raise SystemExit("未能在 SSE 响应中找到 data 行")
        return json.loads(raw)


def main() -> int:
    parser = argparse.ArgumentParser(description="GitHub MCP 通用调用器")
    parser.add_argument("--tool", default="")
    parser.add_argument("--args", default="{}")
    parser.add_argument("--args-file", default="", help="从文件读取工具参数（避免命令行引号被剥离）")
    parser.add_argument("--list", action="store_true")
    parser.add_argument("--transcript", default="")
    parser.add_argument("--endpoint", default=ENDPOINT)
    args = parser.parse_args()

    token = os.environ.get("GITHUB_PERSONAL_ACCESS_TOKEN", "").strip()
    if not token:
        raise SystemExit("缺少环境变量 GITHUB_PERSONAL_ACCESS_TOKEN（令牌只经环境变量传入）")

    transcript: list = []
    client = MCPClient(args.endpoint, token, transcript)
    client.post("initialize", {
        "protocolVersion": PROTOCOL_VERSION,
        "capabilities": {},
        "clientInfo": {"name": "mcode-session", "version": "1.0"},
    })
    client.post("notifications/initialized", {}, notification=True)

    tools = client.post("tools/list", {})
    available = [t.get("name") for t in (tools or {}).get("result", {}).get("tools", [])]
    if args.list:
        print("\n".join(available))
        return 0
    if not args.tool:
        raise SystemExit("请指定 --tool 或 --list")

    raw_args = pathlib.Path(args.args_file).read_text(encoding="utf-8") if args.args_file else args.args
    result = client.post("tools/call", {"name": args.tool, "arguments": json.loads(raw_args)})
    print(json.dumps(result, ensure_ascii=False, indent=2)[:6000])

    if args.transcript:
        path = pathlib.Path(args.transcript)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps({
            "generated_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
            "endpoint": args.endpoint,
            "mcp_protocol_version": PROTOCOL_VERSION,
            "tool": args.tool,
            "note": "令牌未写入本文件；请求头中的 Authorization 已整体省略。",
            "transcript": transcript,
        }, ensure_ascii=False, indent=2), encoding="utf-8")
        print(f"transcript 已写入 {path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
