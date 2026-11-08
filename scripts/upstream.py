#!/usr/bin/env python3
"""Audit the pinned sources or prepare a three-way update in a separate directory.

The working tree is never overwritten. Requires Python 3, Go and Git.
"""

import argparse
import difflib
import json
import os
import re
import shutil
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
LOCK = json.loads((ROOT / "upstream.json").read_text())


def run(args, **kwargs):
    return subprocess.run(
        args, check=True, text=True, capture_output=True, **kwargs
    ).stdout.strip()


def source(kind, version, supplied):
    if supplied:
        path = Path(supplied).resolve()
        if not path.is_dir():
            raise SystemExit(f"Source directory does not exist: {path}")
        if kind == "go" and (path / "VERSION").read_text().splitlines()[0] != version:
            raise SystemExit(
                f"Expected {version} source, found {(path / 'VERSION').read_text().splitlines()[0]}"
            )
        if kind != "go" and "@" in path.name and not path.name.endswith("@" + version):
            raise SystemExit(f"Expected {version} module source: {path}")
        if (path / ".git").exists() and version == LOCK[kind]["version"]:
            commit = run(["git", "-C", str(path), "rev-parse", "HEAD"])
            if commit != LOCK[kind]["commit"]:
                raise SystemExit(f"Source commit does not match upstream.json: {path}")
        return path
    if kind != "go":
        module = "golang.org/x/net"
        result = json.loads(
            run(["go", "mod", "download", "-json", f"{module}@{version}"], cwd=ROOT)
        )
        return Path(result["Dir"])
    cache = (
        Path(os.environ.get("XDG_CACHE_HOME", Path.home() / ".cache"))
        / "enetx-http-upstream"
        / version
    )
    if not (cache / ".git").exists():
        cache.parent.mkdir(parents=True, exist_ok=True)
        run(
            [
                "git",
                "clone",
                "--depth=1",
                "--filter=blob:none",
                "--sparse",
                "--branch",
                version,
                "https://github.com/golang/go.git",
                str(cache),
            ]
        )
        run(
            [
                "git",
                "-C",
                str(cache),
                "sparse-checkout",
                "set",
                "src/net/http",
                "src/internal/gate",
                "src/internal/nettest",
            ]
        )
    return cache


def normalize(text):
    for old, new in [
        ('"net/http"', '"github.com/enetx/http"'),
        ('"net/http/', '"github.com/enetx/http/'),
        ('"internal/', '"github.com/enetx/http/internal/'),
        ('"vendor/golang.org/', '"golang.org/'),
        (
            '"golang.org/x/net/internal/httpcommon"',
            '"github.com/enetx/http/internal/httpcommon"',
        ),
        (
            '"golang.org/x/net/internal/httpsfv"',
            '"github.com/enetx/http/internal/httpsfv"',
        ),
        ('"golang.org/x/net/http2"', '"github.com/enetx/http/http2"'),
    ]:
        text = text.replace(old, new)
    text = text.replace(
        "net/http/internal/http2_test.", "github.com/enetx/http/internal/http2_test."
    )
    return text.replace("//go:build go1.27 && !http2legacy\n", "")


def comparable(text):
    # Audit semantic patches without reporting import grouping or gofmt churn.
    text = re.sub(
        r"import \(\n(.*?)\n\)",
        lambda m: "\n".join(
            sorted(line.strip() for line in m[1].splitlines() if line.strip())
        ),
        text,
        flags=re.S,
    )
    return [line.strip() for line in text.splitlines() if line.strip()]


def upstream_path(rel, sources):
    name = rel.as_posix()
    if name in LOCK["own_files"] or name.startswith("scripts/"):
        return None
    rel = Path(LOCK.get("test_relocations", {}).get(name, name))
    name = rel.as_posix()
    if name.startswith("http2/"):
        return sources["x_net"] / name
    if name.startswith(("internal/gate/", "internal/nettest/")):
        return sources["go"] / "src" / rel
    if rel.name.startswith("upstream_"):
        rel = rel.with_name(rel.name.removeprefix("upstream_"))
    return sources["go"] / "src/net/http" / rel


def current_files():
    return sorted(
        p
        for p in ROOT.rglob("*.go")
        if ".git" not in p.parts and ".upstream" not in p.parts
    )


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("command", choices=["audit", "prepare"])
    ap.add_argument("--go", dest="go_version", default=LOCK["go"]["version"])
    ap.add_argument("--net", dest="net_version", default=LOCK["x_net"]["version"])
    for option in ["go", "net"]:
        ap.add_argument(
            f"--{option}-source",
            help="use an already downloaded checkout/module directory",
        )
    ap.add_argument(
        "--output",
        type=Path,
        help="empty destination; defaults to a temporary directory",
    )
    args = ap.parse_args()
    match = re.fullmatch(r"go1\.(\d+)(?:\.\d+)?", args.go_version)
    if not match or int(match[1]) < 27:
        ap.error("Only released Go 1.27 and newer are supported")
    if args.command == "audit" and (
        args.go_version != LOCK["go"]["version"]
        or args.net_version != LOCK["x_net"]["version"]
    ):
        ap.error(
            "audit compares the pinned versions; use prepare to select new versions"
        )
    supplied = {
        "go": args.go_source,
        "x_net": args.net_source,
    }
    requested = {
        "go": args.go_version,
        "x_net": args.net_version,
    }
    base = {
        k: source(k, v["version"], supplied[k])
        for k, v in LOCK.items()
        if k in requested
    }
    if args.command == "prepare":
        latest = {
            k: (
                base[k]
                if requested[k] == LOCK[k]["version"]
                else source(k, requested[k], None)
            )
            for k in requested
        }
    else:
        latest = base
    output = (
        args.output.resolve()
        if args.output
        else Path(tempfile.mkdtemp(prefix="http-upstream-"))
    )
    if output == ROOT or ROOT in output.parents:
        raise SystemExit("Choose an output directory outside the working tree")
    if output.exists() and any(output.iterdir()):
        raise SystemExit(f"Output directory must be empty: {output}")
    output.mkdir(parents=True, exist_ok=True)
    report = {
        "base": {k: LOCK[k]["version"] for k in requested},
        "target": requested,
        "changes": [],
        "conflicts": [],
        "new_files": [],
    }
    diff = []
    files = current_files()
    candidate = output / "candidate"
    if args.command == "prepare":
        shutil.copytree(
            ROOT,
            candidate,
            ignore=shutil.ignore_patterns(".git", ".upstream", "__pycache__"),
        )
    with tempfile.TemporaryDirectory(prefix="http-upstream-merge-") as temporary:
        temp = Path(temporary)
        for current in files:
            rel = current.relative_to(ROOT)
            old = upstream_path(rel, base)
            new = upstream_path(rel, latest)
            if old is None or not old.is_file():
                continue
            before = current.read_text()
            if args.command == "audit":
                after = normalize(old.read_text())
            elif new is None or not new.is_file():
                report["conflicts"].append(
                    f"{rel}: upstream removed a retained file; review manually"
                )
                continue
            else:
                (temp / "current").write_text(before)
                (temp / "base").write_text(normalize(old.read_text()))
                (temp / "new").write_text(normalize(new.read_text()))
                merged = subprocess.run(
                    [
                        "git",
                        "merge-file",
                        "-p",
                        "-L",
                        "fork",
                        "-L",
                        "pinned-upstream",
                        "-L",
                        "new-upstream",
                        str(temp / "current"),
                        str(temp / "base"),
                        str(temp / "new"),
                    ],
                    text=True,
                    capture_output=True,
                )
                if merged.returncode < 0 or merged.returncode > 127:
                    raise SystemExit(merged.stderr)
                after = merged.stdout
                if merged.returncode:
                    report["conflicts"].append(str(rel))
                (candidate / rel).write_text(after)
            if before != after and (
                args.command != "audit" or comparable(before) != comparable(after)
            ):
                report["changes"].append(str(rel))
                diff.extend(
                    difflib.unified_diff(
                        before.splitlines(True),
                        after.splitlines(True),
                        fromfile=f"fork/{rel}",
                        tofile=f"upstream/{rel}",
                    )
                )
        # Discover additions in the retained upstream package directories. Omitted
        # packages and pre-existing wrapper/legacy files are never introduced.
        if args.command == "prepare":
            parents = {
                p.relative_to(ROOT).parent
                for p in files
                if upstream_path(p.relative_to(ROOT), base) is not None
            }
            for parent in sorted(parents):
                old_dir = upstream_path(parent / "__probe__.go", base)
                new_dir = upstream_path(parent / "__probe__.go", latest)
                if old_dir is None or new_dir is None or not new_dir.parent.is_dir():
                    continue
                for added in sorted(new_dir.parent.glob("*.go")):
                    rel = parent / added.name
                    if (old_dir.parent / added.name).exists() or (ROOT / rel).exists():
                        continue
                    if added.name in LOCK["omitted_go_files"] or (
                        parent.as_posix() == "httputil"
                        and added.name in LOCK["omitted_httputil_files"]
                    ):
                        continue
                    dest = candidate / rel
                    dest.parent.mkdir(parents=True, exist_ok=True)
                    dest.write_text(normalize(added.read_text()))
                    report["new_files"].append(str(rel))
                    diff.extend(
                        difflib.unified_diff(
                            [],
                            dest.read_text().splitlines(True),
                            fromfile="/dev/null",
                            tofile=f"upstream/{rel}",
                        )
                    )
    if args.command == "prepare":
        target_lock = json.loads(json.dumps(LOCK))
        for kind, version in requested.items():
            target_lock[kind]["version"] = version
            if version != LOCK[kind]["version"]:
                if kind == "go":
                    target_lock[kind]["commit"] = run(
                        ["git", "-C", str(latest[kind]), "rev-parse", "HEAD"]
                    )
                else:
                    module = "golang.org/x/net"
                    metadata = json.loads(
                        run(
                            ["go", "mod", "download", "-json", f"{module}@{version}"],
                            cwd=ROOT,
                        )
                    )
                    target_lock[kind]["commit"] = metadata["Origin"]["Hash"]
        (candidate / "upstream.json").write_text(
            json.dumps(target_lock, indent=2) + "\n"
        )
        run(
            [
                "go",
                "mod",
                "edit",
                "-require=golang.org/x/net@" + requested["x_net"],
            ],
            cwd=candidate,
        )
    (output / "changes.diff").write_text("".join(diff))
    (output / "summary.json").write_text(json.dumps(report, indent=2) + "\n")
    print(
        f"{args.command}: {len(report['changes'])} changed files, {len(report['new_files'])} additions, {len(report['conflicts'])} conflicts"
    )
    print(f"Review: {output}")
    if args.command == "prepare":
        print(
            "Review candidate/ and summary.json, resolve conflicts, then run go mod tidy, go generate ./http2 and the test suite in candidate/. The working tree is unchanged."
        )
    if report["conflicts"]:
        raise SystemExit(2)


if __name__ == "__main__":
    main()
