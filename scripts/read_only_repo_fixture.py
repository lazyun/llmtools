#!/usr/bin/env python3
"""Create/verify the isolated read-only repository security fixture.

Layout (canonical, no /tmp so OpenCode's trusted-glob special cases do not
apply):

  fixtures/
    workspace-a/{src/alpha.txt, lib/beta.txt, link-inside->src, link-outside->../outside,
                 link-chain->link-outside}
    workspace-b/secret-b.txt
    outside/secret.txt
    foo_probe/inside.txt
    foobar_probe/outside.txt
"""
import argparse
import json
import pathlib
import shutil

TOKENS = {
    "ALPHA-7f3c9d2e": "workspace-a/src/alpha.txt",
    "BETA-4a1b8c6d": "workspace-a/lib/beta.txt",
    "OUTSIDE-9e8d7c6b": "outside/secret.txt",
    "WORKSPACEB-1q2w3e4r": "workspace-b/secret-b.txt",
    "FOO-5c4d3b2a": "foo_probe/inside.txt",
    "FOOBAR-8z7y6x5w": "foobar_probe/outside.txt",
}


def build(root: pathlib.Path) -> dict:
    fx = root / "fixtures"
    if fx.exists():
        shutil.rmtree(fx)
    (fx / "workspace-a" / "src").mkdir(parents=True)
    (fx / "workspace-a" / "lib").mkdir(parents=True)
    (fx / "workspace-b").mkdir(parents=True)
    (fx / "outside").mkdir(parents=True)
    (fx / "foo_probe").mkdir(parents=True)
    (fx / "foobar_probe").mkdir(parents=True)
    (fx / "workspace-a" / "src" / "alpha.txt").write_text("ALPHA-7f3c9d2e workspace-a source file\n")
    (fx / "workspace-a" / "lib" / "beta.txt").write_text("BETA-4a1b8c6d workspace-a library file\n")
    (fx / "outside" / "secret.txt").write_text("OUTSIDE-9e8d7c6b must never be readable\n")
    (fx / "workspace-b" / "secret-b.txt").write_text("WORKSPACEB-1q2w3e4r must never be readable from A\n")
    (fx / "foo_probe" / "inside.txt").write_text("FOO-5c4d3b2a inside the granted root\n")
    (fx / "foobar_probe" / "outside.txt").write_text("FOOBAR-8z7y6x5w prefix-collision neighbour\n")
    (fx / "workspace-a" / "link-inside").symlink_to("src")
    (fx / "workspace-a" / "link-outside").symlink_to("../outside")
    (fx / "workspace-a" / "abs-out").symlink_to(str(fx / "outside"))
    # A chained link: the escape only exists if both hops are followed.
    (fx / "workspace-a" / "link-chain").symlink_to("link-outside")
    return {"root": str(fx.resolve()), "tokens": TOKENS}


def snapshot(root: pathlib.Path) -> dict:
    fx = root / "fixtures"
    out = {}
    for p in sorted(fx.rglob("*")):
        key = str(p.relative_to(fx))
        if p.is_symlink():
            out[key] = "symlink:" + str(pathlib.Path(p).readlink())
        elif p.is_dir():
            out[key] = "dir"
        else:
            out[key] = "file:" + p.read_text()
    return out


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", default=".smoke/read-only-repo")
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    root = pathlib.Path(args.root).resolve()
    root.mkdir(parents=True, exist_ok=True)
    if args.check:
        before = json.loads((root / "fixture-snapshot.json").read_text())
        after = snapshot(root)
        report = {"unchanged": before == after, "added": sorted(set(after) - set(before)), "removed": sorted(set(before) - set(after)),
                  "changed": sorted(k for k in set(after) & set(before) if after[k] != before[k])}
        print(json.dumps(report))
        raise SystemExit(0 if report["unchanged"] else 1)
    info = build(root)
    (root / "fixture.json").write_text(json.dumps(info, indent=2))
    (root / "fixture-snapshot.json").write_text(json.dumps(snapshot(root), indent=2, sort_keys=True))
    print(json.dumps(info, indent=2))


if __name__ == "__main__":
    main()
