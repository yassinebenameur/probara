"""Dependency selection, package ownership and structured Go test reports for CI."""
import argparse
from collections import defaultdict, deque
import json
import os
from pathlib import Path
import subprocess
import sys

MODULE = "github.com/yassinebenameur/probara/"
SHARDS = ("api", "alerting-worker", "platform")
# These tests explicitly generate local previews or validate an optional binary.
# Docker and Chrome skips are deliberately not allowed in the full CI suite.
ALLOWED_SKIPS = {
    ("api/internal/services/agent", "TestGeneratedConfigValidatesWithCollector"):
        "Collector binaries are validated after make build-collector-static, not built in this workflow.",
    ("shared/notifications/plugin/builtin/email", "TestDumpPreviews"):
        "Opt-in local email preview generator.",
    ("status-page/internal/statuspage", "TestWritePreviewHTML"):
        "Opt-in local status-page preview generator.",
}


def shard_for(package):
    if not package.startswith(MODULE):
        raise ValueError(f"package outside the module: {package}")
    path = package[len(MODULE):]
    if path == "api" or path.startswith("api/"):
        if path in ("api/internal/services/dashboard", "api/internal/services/slas"):
            return "platform"
        return "api"
    if path.split("/")[0] in ("alerter", "worker"):
        return "alerting-worker"
    # New packages are always covered; no manually maintained package allow-list.
    return "platform"


def ai_validation_needed(event, paths):
    # Protected-branch pushes always reproduce the full release gate. Unknown
    # events/diffs are conservative, and renames include both sides of the diff.
    if event != "pull_request" or paths is None:
        return True
    prefixes = ("ai-service/", "shared/ai/", "helm/", ".github/workflows/", "scripts/local-ai")
    exact = {".gitattributes", ".dockerignore", "pytest.ini", "scripts/check-local-ai-packaging.py", "Makefile",
             "docker-compose.yml", "scripts/start-local-services.sh"}
    return any(path.startswith(prefixes) or path in exact for path in paths)


def changed_paths(base, head):
    try:
        # --no-renames reports both the deleted and added names, including moves
        # out of an AI directory. Arguments are passed directly, never via a shell.
        result = subprocess.run(["git", "diff", "--no-renames", "--name-only", "-z",
                                 f"{base}...{head}", "--"], check=True, capture_output=True)
        return [os.fsdecode(path) for path in result.stdout.split(b"\0") if path]
    except subprocess.CalledProcessError:
        return None


def annotation(message):
    return message.replace("%", "%25").replace("\r", "%0D").replace("\n", "%0A")


def go_report(lines):
    events, raw_errors = [], []
    for line in lines:
        try:
            event = json.loads(line)
        except (ValueError, TypeError):
            if line.strip():
                raw_errors.append(line.rstrip())
            continue
        if isinstance(event, dict):
            events.append(event)
    history = defaultdict(lambda: deque(maxlen=35))
    failures, skips, timings = [], [], []
    for event in events:
        package, test = event.get("Package", event.get("ImportPath", "")), event.get("Test", "")
        if "Output" in event:
            history[package, test].append(event["Output"].rstrip())
        action = event.get("Action")
        if action in ("fail", "build-fail"):
            failures.append((package, test, "\n".join(history[package, test])))
        if action == "skip" and test:
            relative = package.removeprefix(MODULE)
            skips.append((relative, test))
            if (relative, test.split("/")[0]) not in ALLOWED_SKIPS:
                failures.append((package, test, "Unexpected skip: a required integration dependency may be missing."))
        if action == "pass" and not test and "Package" in event:
            timings.append((package.removeprefix(MODULE), event.get("Elapsed", 0)))
    if not timings:
        failures.append(("test runner", "", "No passing package results found."))
    # Transport/Go tool errors outside test2json must not disappear in a report.
    if raw_errors:
        failures.append(("test runner", "", "\n".join(raw_errors[-35:])))
    return failures, skips, sorted(timings, key=lambda pair: -pair[1])


def merge_coverage(paths):
    blocks = defaultdict(int)
    for path in paths:
        lines = Path(path).read_text(encoding="utf-8").splitlines()
        if not lines or lines[0] != "mode: atomic":
            raise ValueError(f"expected atomic coverage: {path}")
        for line in lines[1:]:
            if not line.strip():
                continue
            block, count = line.rsplit(" ", 1)
            value = int(count)
            if value < 0:
                raise ValueError(f"negative coverage count: {path}")
            blocks[block] += value
    if not blocks:
        raise ValueError("empty coverage profiles")
    return "mode: atomic\n" + "".join(f"{block} {blocks[block]}\n" for block in sorted(blocks))


def required_failures(results, suite="all"):
    groups = {"go": {"go-format", "go-unit", "go-full"},
              "ai": {"changes", "ai-fast", "ai-checkpoint"}}
    required = groups["go"] | groups["ai"] if suite == "all" else groups[suite]
    failures = [f"missing result: {name}" for name in sorted(required - results.keys())]
    decision = results.get("changes", {}).get("outputs", {}).get("ai_validation")
    if suite != "go" and decision not in ("true", "false"):
        failures.append("missing AI validation decision")
    for name, job in results.items():
        if job["result"] == "success":
            continue
        if name == "ai-checkpoint" and decision == "false" and job["result"] == "skipped":
            continue
        failures.append(f"{name}: {job['result']}")
    return failures


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    changes = commands.add_parser("changes")
    changes.add_argument("--event", required=True)
    changes.add_argument("--base", default="")
    changes.add_argument("--head", default="HEAD")
    packages = commands.add_parser("packages")
    packages.add_argument("--shard", choices=SHARDS, required=True)
    report = commands.add_parser("go-report")
    report.add_argument("log", type=Path)
    coverage = commands.add_parser("merge-coverage")
    coverage.add_argument("directory", type=Path)
    coverage.add_argument("output", type=Path)
    required = commands.add_parser("required-results")
    required.add_argument("--suite", choices=("go", "ai", "all"), default="all")
    args = parser.parse_args()
    if args.command == "changes":
        paths = changed_paths(args.base, args.head) if args.event == "pull_request" and args.base else None
        needed = ai_validation_needed(args.event, paths)
        value = f"ai_validation={'true' if needed else 'false'}\n"
        if os.environ.get("GITHUB_OUTPUT"):
            with open(os.environ["GITHUB_OUTPUT"], "a") as output:
                output.write(value)
        print(value, end="")
    elif args.command == "packages":
        all_packages = subprocess.check_output(["go", "list", "./..."], text=True).splitlines()
        selected = [package for package in all_packages if shard_for(package) == args.shard]
        if not selected:
            raise ValueError(f"empty package group: {args.shard}")
        print("\n".join(selected))
    elif args.command == "go-report":
        failures, skips, timings = go_report(args.log.read_text(encoding="utf-8").splitlines())
        summary = ["### Go test results", "", "| Package | Seconds |", "|---|---:|"]
        summary += [f"| `{package}` | {seconds:.2f} |" for package, seconds in timings[:20]]
        summary += ["", f"Passing packages: {len(timings)}. Skipped cases: {len(skips)}."]
        summary += [f"- Skipped `{package}/{test}`: {ALLOWED_SKIPS.get((package, test.split('/')[0]), 'see test log')}"
                    for package, test in skips]
        for package, test, detail in failures:
            print(f"::error title=Go test failure::{annotation(package + ' ' + test + ': ' + detail)}")
            summary += ["", f"**Failed:** `{package} {test}`", "```text", detail, "```"]
        print("\n".join(summary))
        if os.environ.get("GITHUB_STEP_SUMMARY"):
            with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as output:
                output.write("\n".join(summary) + "\n")
        return int(bool(failures))
    elif args.command == "merge-coverage":
        paths = sorted(args.directory.glob("coverage-*.out"))
        expected = {f"coverage-{shard}.out" for shard in SHARDS}
        if {path.name for path in paths} != expected:
            raise ValueError(f"expected coverage profiles {sorted(expected)}, found {[path.name for path in paths]}")
        args.output.write_text(merge_coverage(paths), encoding="utf-8")
    else:
        failures = required_failures(json.loads(os.environ["CI_RESULTS"]), args.suite)
        if failures:
            print("Required CI results failed: " + ", ".join(failures), file=sys.stderr)
            return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
