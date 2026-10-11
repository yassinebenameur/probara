"""Rewrite the --hash lines of ai-service lock files from their name==version pins.

The pins are the source of truth; this only (re)derives hashes, for every
wheel a supported install can pick: CPython 3.13 (not free-threaded) on glibc
Linux x86_64/aarch64 (image, CI) and macOS (development). torch hashes come
from the PyTorch CPU index for Linux, so a hash-checked Linux install can
never resolve the CUDA build from PyPI, and from PyPI for macOS (CPU only).

    python ai-service/tools/lock_hashes.py            # rewrite in place
    python ai-service/tools/lock_hashes.py --check    # exit 1 on drift
"""
import argparse
import json
from pathlib import Path
import re
import sys
import urllib.request

from packaging.tags import parse_tag
from packaging.utils import canonicalize_name, parse_wheel_filename

ROOT = Path(__file__).resolve().parents[1]
LOCKS = ("requirements.lock", "requirements-dev.lock", "requirements-fetch.lock", "requirements-build.lock")
TORCH_CPU_INDEX = "https://download.pytorch.org/whl/cpu/torch/"
PIN = re.compile(r"^([A-Za-z0-9_.-]+)==([^\s\\;]+)")


def supported(tag):
    if tag.interpreter not in ("cp313", "py3", "py2", "py313") and not (
            tag.abi == "abi3" and tag.interpreter.startswith("cp3") and int(tag.interpreter[3:]) <= 13):
        return False
    if tag.abi.endswith("t"):  # free-threaded build
        return False
    p = tag.platform
    return p == "any" or p.startswith("macosx_") or (
        p.startswith("manylinux") and (p.endswith("_x86_64") or p.endswith("_aarch64")))


def wheel_ok(filename):
    return any(supported(tag) for tag in parse_wheel_filename(filename)[3])


def pypi_files(name, version):
    with urllib.request.urlopen(f"https://pypi.org/pypi/{name}/{version}/json", timeout=60) as response:
        data = json.load(response)
    return {f["filename"]: f["digests"]["sha256"] for f in data["urls"] if f["packagetype"] == "bdist_wheel"}


def torch_hashes(version):
    with urllib.request.urlopen(TORCH_CPU_INDEX, timeout=60) as response:
        page = response.read().decode()
    linux = {}
    for name, digest in re.findall(r'href="[^"]*?/(torch-[^"#/]+\.whl)#sha256=([0-9a-f]{64})"', page):
        name = name.replace("%2B", "+")
        _, wheel_version, _, tags = parse_wheel_filename(name)
        if wheel_version.public == version and any(supported(t) and t.platform.startswith("manylinux") for t in tags):
            linux[name] = digest
    mac = {n: d for n, d in pypi_files("torch", version).items() if "macosx" in n and wheel_ok(n)}
    if not any("x86_64" in n for n in linux) or not any("aarch64" in n for n in linux) or not mac:
        raise SystemExit(f"torch {version}: missing CPU wheels for a supported platform")
    return sorted({*linux.values(), *mac.values()})


def hashes(name, version):
    if canonicalize_name(name) == "torch":
        return torch_hashes(version)
    files = {n: d for n, d in pypi_files(name, version).items() if wheel_ok(n)}
    linux = [n for n in files if "manylinux" in n or n.endswith("-any.whl")]
    if not any("x86_64" in n or n.endswith("-any.whl") for n in linux) or not any("aarch64" in n or n.endswith("-any.whl") for n in linux):
        raise SystemExit(f"{name}=={version}: no CPython 3.13 wheel for both Linux x86_64 and aarch64")
    return sorted(set(files.values()))


def render(path, cache):
    lines, out = path.read_text().splitlines(), []
    for line in lines:
        stripped = line.strip()
        if stripped.startswith("--hash="):
            continue
        match = PIN.match(stripped)
        if not match:
            out.append(line.rstrip())
            continue
        key = (canonicalize_name(match[1]), match[2])
        if key not in cache:
            cache[key] = hashes(match[1], match[2])
        out.append(f"{match[1]}=={match[2]} \\")
        out.extend(f"    --hash=sha256:{d} \\" for d in cache[key][:-1])
        out.append(f"    --hash=sha256:{cache[key][-1]}")
    return "\n".join(out) + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--check", action="store_true")
    args, cache, drift = parser.parse_args(), {}, []
    for name in LOCKS:
        path = ROOT / name
        text = render(path, cache)
        if text != path.read_text():
            drift.append(name)
            if not args.check:
                path.write_text(text)
    if args.check and drift:
        print("Lock hashes out of date: " + ", ".join(drift), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
