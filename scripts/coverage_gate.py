#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""覆盖率门禁脚本（本项目唯一的覆盖率统计实现，CI 与本机共用）。

用法：
    python scripts/coverage_gate.py --root . --out-dir evidence --min 85

要点：
1. **逐包**执行 `go test <pkg> -coverprofile` 再汇总：直接用 `go test ./... -coverprofile`
   会把多个测试二进制的剖面合并，同一个包的代码块（例如被 cmd/server 的测试间接执行到的
   httpapi 代码）会被重复写入，合并后的 numstmt 出现重复累加，导致同一份代码在不同机器上
   得到不同百分比（实测本机 89.51% vs Linux CI 84.26%）。逐包取剖面后口径唯一且可比。
2. 先删除旧剖面，再产出**绝对路径**的新剖面（相对路径会静默写到包目录，读到陈旧剖面）；
3. 统计口径直接解析覆盖率剖面，按语句数统计 covered/total，与 go tool cover 一致；
4. 整体覆盖率与各核心业务包覆盖率都必须达到阈值，否则退出码非 0；
5. 不排除任何自编包：自编的数据库访问与 HTTP 处理代码都在统计范围内。
"""
from __future__ import annotations

import argparse
import collections
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


def list_packages(root: pathlib.Path) -> list[str]:
    out = run(["go", "list", "./..."], root)
    return [line.strip() for line in out.splitlines() if line.strip()]


def read_blocks(path: pathlib.Path):
    """逐行读取剖面，产出 (块键, 文件, 包名, 语句数, 执行次数)。"""
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
            key = (normalized, normalized.rsplit(":", 1)[-1])
            yield key, normalized, package, total, int(count)


def measure(root: pathlib.Path, out_dir: pathlib.Path):
    """逐包取覆盖率剖面并汇总，返回 ({包名: (语句覆盖, 语句总数)}, {包名: (块覆盖, 块总数)}, 合并剖面文本)。

    同时统计两种口径：
    * 语句口径：按剖面 numstmt 统计（题面要求的「语句覆盖率」），但**不同 Go 工具链对 numstmt
      的统计口径不一致**（实测 go1.27.1/windows 比 go1.27.4/linux 偏大），因此不能单独依赖它；
    * 块口径：已覆盖代码块数 / 总代码块数，**跨平台完全一致**，作为保守下界。
    门禁要求两种口径同时达标，避免「本机通过、CI 失败」。
    """
    work = out_dir / ".coverpkg"
    work.mkdir(parents=True, exist_ok=True)
    merged: dict[tuple[str, str], list] = {}
    for pkg in list_packages(root):
        safe = pkg.replace("/", "_")
        profile = (work / f"{safe}.out").resolve()
        if profile.exists():
            profile.unlink()
        run(["go", "test", pkg, "-count=1", "-covermode=atomic", f"-coverprofile={profile}"], root)
        if not profile.exists():
            raise SystemExit(f"包 {pkg} 的覆盖率剖面未生成")
        for key, normalized, package, total, count in read_blocks(profile):
            previous = merged.get(key)
            if previous is None:
                merged[key] = [normalized, package, total, count]
            else:
                # 同一块在多个剖面出现时，语句数取一致值，执行次数取最大值
                previous[2] = max(previous[2], total)
                previous[3] = max(previous[3], count)
    counts = collections.defaultdict(lambda: [0, 0])
    blocks = collections.defaultdict(lambda: [0, 0])
    lines = ["mode: atomic"]
    for key in sorted(merged):
        normalized, package, total, count = merged[key]
        blocks[package][1] += 1
        if count > 0:
            counts[package][0] += total
            blocks[package][0] += 1
        counts[package][1] += total
        lines.append(f"{normalized} {total} {count}")
    return counts, blocks, "\n".join(lines) + "\n"


def parse_profile(path: pathlib.Path):
    """只统计已有剖面（--skip-tests），按块去重。"""
    merged: dict[tuple[str, str], list] = {}
    for key, normalized, package, total, count in read_blocks(path):
        previous = merged.get(key)
        if previous is None:
            merged[key] = [package, total, count]
        else:
            previous[1] = max(previous[1], total)
            previous[2] = max(previous[2], count)
    counts = collections.defaultdict(lambda: [0, 0])
    for package, total, count in merged.values():
        if count > 0:
            counts[package][0] += total
        counts[package][1] += total
    return counts


def block_counts(path: pathlib.Path):
    """按包统计 (已覆盖块数, 总块数)。"""
    merged: dict[tuple[str, str], list] = {}
    for key, normalized, package, total, count in read_blocks(path):
        merged[key] = [package, count]
    blocks = collections.defaultdict(lambda: [0, 0])
    for package, count in merged.values():
        blocks[package][1] += 1
        if count > 0:
            blocks[package][0] += 1
    return blocks


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

    if args.skip_tests:
        # 复算模式：只统计**已有**剖面，不得删除它（删除会让复算必然失败并毁掉证据文件）。
        if not profile.exists():
            sys.stderr.write("覆盖率剖面不存在（不能沿用旧文件）\n")
            return 2
        counts = parse_profile(profile)
        blocks = block_counts(profile)
    else:
        # 关键：先删除旧剖面，避免读到陈旧数据
        if profile.exists():
            profile.unlink()
        counts, blocks, merged_text = measure(root, out_dir)
        profile.write_text(merged_text, encoding="utf-8", newline="\n")

    if not counts:
        sys.stderr.write("覆盖率剖面为空\n")
        return 2

    total_covered = sum(v[0] for v in counts.values())
    total_statements = sum(v[1] for v in counts.values())
    overall = pct(total_covered, total_statements)
    block_covered = sum(v[0] for v in blocks.values())
    block_total = sum(v[1] for v in blocks.values())
    overall_block = pct(block_covered, block_total)

    lines = []
    lines.append("题目 06 单元测试覆盖率门禁报告")
    lines.append("=" * 60)
    lines.append(f"覆盖率剖面：{profile}")
    lines.append("统计口径：逐包执行 go test -coverprofile 后按块去重汇总")
    lines.append("  · 语句口径：按剖面 numstmt 统计（题面要求的语句覆盖率）")
    lines.append("  · 块口径：已覆盖代码块 / 总代码块，跨平台一致，作为保守下界")
    lines.append("  · 两种口径同时达标才算通过（避免本机通过而 CI 失败）")
    lines.append("排除项：无（自编的数据库访问与 HTTP 处理代码全部纳入统计）")
    lines.append("")
    lines.append("【各包覆盖率】")
    for pkg in sorted(counts):
        covered, total = counts[pkg]
        bcovered, btotal = blocks.get(pkg, [0, 0])
        lines.append(f"  {pkg:<40} 语句 {pct(covered, total):6.2f}%  块 {pct(bcovered, btotal):6.2f}%"
                     f"  ({covered}/{total} ; {bcovered}/{btotal})")
    lines.append("")
    lines.append(f"【整体覆盖率】 语句 {overall:.2f}%  块 {overall_block:.2f}%"
                 f"  ({total_covered}/{total_statements} ; {block_covered}/{block_total})")

    failures = []
    if overall < args.min:
        failures.append(f"整体语句覆盖率 {overall:.2f}% < {args.min}%")
    if overall_block < args.min:
        failures.append(f"整体块覆盖率 {overall_block:.2f}% < {args.min}%")
    for pkg in CORE_PACKAGES:
        if pkg not in counts:
            failures.append(f"核心包 {pkg} 未出现在覆盖率剖面中")
            continue
        covered, total = counts[pkg]
        value = pct(covered, total)
        bcovered, btotal = blocks.get(pkg, [0, 0])
        bvalue = pct(bcovered, btotal)
        lines.append(f"  核心包 {pkg:<34} 语句 {value:6.2f}%  块 {bvalue:6.2f}%")
        if value < args.min:
            failures.append(f"核心包 {pkg} 语句覆盖率 {value:.2f}% < {args.min}%")
        if bvalue < args.min:
            failures.append(f"核心包 {pkg} 块覆盖率 {bvalue:.2f}% < {args.min}%")

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
