#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""覆盖率门禁脚本（本项目唯一的覆盖率统计实现，CI 与本机共用）。

用法：
    python scripts/coverage_gate.py --root . --out-dir evidence --min 85

要点：
1. 先删除旧剖面，再产出**绝对路径**的新剖面（相对路径会静默写到包目录，读到陈旧剖面）；
2. 统计口径直接解析覆盖率剖面，按语句数统计 covered/total，与 go tool cover 一致；
3. 整体覆盖率与各核心业务包覆盖率都必须达到阈值，否则退出码非 0；
4. 不排除任何自编包：自编的数据库访问与 HTTP 处理代码都在统计范围内。
"""
from __future__ import annotations

import argparse
import collections
import os
import pathlib
import subprocess
import sys

CORE_PACKAGES = [
    "creative-studio/internal/domain",
    "creative-studio/internal/store",
    "creative-studio/internal/service",
    "creative-studio/internal/httpapi",
    "creative-studio/internal/supplier",
]


def run(cmd: list[str], cwd: pathlib.Path) -> str:
    proc = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, encoding="utf-8", errors="replace")
    if proc.returncode != 0:
        sys.stderr.write(proc.stdout)
        sys.stderr.write(proc.stderr)
        raise SystemExit(f"命令失败：{' '.join(cmd)}")
    return proc.stdout


def parse_profile(path: pathlib.Path):
    """解析覆盖率剖面，返回 {包名: (covered, total)}。"""
    counts = collections.defaultdict(lambda: [0, 0])
    with path.open(encoding="utf-8") as handle:
        for line in handle:
            line = line.strip()
            if not line or line.startswith("mode:"):
                continue
            location, statements, count = line.rsplit(" ", 2)
            normalized = location.replace("\\", "/")
            package = "/".join(normalized.split("/")[:-1])
            total = int(statements)
            if total == 0:
                continue
            # 一个代码块只要被执行过一次（count > 0）即视为已覆盖；
            # 不能把执行次数累加，否则覆盖率会超过 100%。
            if int(count) > 0:
                counts[package][0] += total
            counts[package][1] += total
    return counts


def pct(covered: int, total: int) -> float:
    if total == 0:
        return 0.0
    return covered * 100.0 / total


def main() -> int:
    parser = argparse.ArgumentParser(description="覆盖率门禁")
    parser.add_argument("--root", default=".", help="仓库根目录")
    parser.add_argument("--out-dir", default="evidence", help="证据输出目录")
    parser.add_argument("--min", type=float, default=85.0, help="整体与各核心包的覆盖率下限")
    parser.add_argument("--skip-tests", action="store_true", help="只统计已有剖面，不重新执行测试")
    args = parser.parse_args()

    root = pathlib.Path(args.root).resolve()
    out_dir = (root / args.out_dir).resolve()
    out_dir.mkdir(parents=True, exist_ok=True)
    profile = (out_dir / "coverage.out").resolve()

    # 关键：先删除旧剖面，避免读到陈旧数据
    if profile.exists():
        profile.unlink()

    if not args.skip_tests:
        run(["go", "test", "./...", "-covermode=atomic", f"-coverprofile={profile}"], root)

    if not profile.exists():
        sys.stderr.write("覆盖率剖面未生成（不能沿用旧文件）\n")
        return 2

    counts = parse_profile(profile)
    if not counts:
        sys.stderr.write("覆盖率剖面为空\n")
        return 2

    total_covered = sum(v[0] for v in counts.values())
    total_statements = sum(v[1] for v in counts.values())
    overall = pct(total_covered, total_statements)

    lines = []
    lines.append("题目 06 单元测试覆盖率门禁报告")
    lines.append("=" * 60)
    lines.append(f"覆盖率剖面：{profile}")
    lines.append(f"统计口径：解析覆盖率剖面按语句数统计 covered/total（与 go tool cover 一致）")
    lines.append(f"排除项：无（自编的数据库访问与 HTTP 处理代码全部纳入统计）")
    lines.append("")
    lines.append("【各包覆盖率】")
    for pkg in sorted(counts):
        covered, total = counts[pkg]
        lines.append(f"  {pkg:<40} {pct(covered, total):6.2f}%  ({covered}/{total})")
    lines.append("")
    lines.append(f"【整体覆盖率】 {overall:.2f}%  ({total_covered}/{total_statements})")

    failures = []
    if overall < args.min:
        failures.append(f"整体覆盖率 {overall:.2f}% < {args.min}%")
    for pkg in CORE_PACKAGES:
        if pkg not in counts:
            failures.append(f"核心包 {pkg} 未出现在覆盖率剖面中")
            continue
        covered, total = counts[pkg]
        value = pct(covered, total)
        lines.append(f"  核心包 {pkg:<34} {value:6.2f}%")
        if value < args.min:
            failures.append(f"核心包 {pkg} 覆盖率 {value:.2f}% < {args.min}%")

    lines.append("")
    if failures:
        lines.append("【结论】FAIL")
        for item in failures:
            lines.append(f"  - {item}")
    else:
        lines.append(f"【结论】PASS（整体与各核心包均 ≥ {args.min}%）")

    report = "\n".join(lines) + "\n"
    (out_dir / "coverage-summary.txt").write_text(report, encoding="utf-8")
    print(report)
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
