import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import ci


class ChangesTest(unittest.TestCase):
    def test_unrelated_pr_does_not_reproduce_checkpoint(self):
        self.assertFalse(ci.ai_validation_needed("pull_request", ["api/internal/mcp/extras.go", "web/lib/api.ts"]))

    def test_inputs_and_unknown_events_keep_validation(self):
        for path in ["ai-service/probara_ai/runtime.py", "shared/ai/catalogue.json",
                     "ai-service/requirements.lock", ".github/workflows/scripts/ci.py",
                     "Makefile", ".gitattributes", ".dockerignore", "pytest.ini",
                     "helm/monitoring-platform/values.yaml", "docker-compose.yml"]:
            with self.subTest(path=path):
                self.assertTrue(ci.ai_validation_needed("pull_request", [path]))
        for event in ["push", "workflow_dispatch", "unknown"]:
            self.assertTrue(ci.ai_validation_needed(event, []))
        self.assertTrue(ci.ai_validation_needed("pull_request", None))

    def test_renames_keep_deleted_dependency_path(self):
        diff = b"ai-service/old.py\0docs/new.py\0docs/with spaces.md\0"
        with patch.object(subprocess, "run", return_value=subprocess.CompletedProcess([], 0, stdout=diff)) as run:
            paths = ci.changed_paths("base", "head")
        self.assertIn("--no-renames", run.call_args.args[0])
        self.assertEqual(paths, ["ai-service/old.py", "docs/new.py", "docs/with spaces.md"])
        self.assertTrue(ci.ai_validation_needed("pull_request", paths))

    def test_unavailable_diff_is_conservative(self):
        with patch.object(subprocess, "run", side_effect=subprocess.CalledProcessError(1, "git")):
            self.assertIsNone(ci.changed_paths("unavailable", "head"))


class PackageGroupsTest(unittest.TestCase):
    def test_service_and_new_package_ownership(self):
        cases = {"api/internal/api": "api", "api/internal/services/monitors": "api",
                 "api/internal/services/dashboard": "platform", "api/internal/services/slas": "platform",
                 "alerter/internal/alerter": "alerting-worker", "worker/internal/worker": "alerting-worker",
                 "scheduler/internal/ingest": "platform", "shared/queue": "platform",
                 "status-page/internal/statuspage": "platform", "cmd/migrate": "platform",
                 "future-service/internal/new": "platform"}
        for package, want in cases.items():
            with self.subTest(package=package):
                self.assertEqual(ci.shard_for(ci.MODULE + package), want)
        with self.assertRaises(ValueError):
            ci.shard_for("another/module")


class ReportsTest(unittest.TestCase):
    def events(self, *events):
        return [json.dumps(event) for event in events]

    def test_failed_case_retains_context_and_package_timing(self):
        failures, skips, timings = ci.go_report(self.events(
            {"Action": "output", "Package": ci.MODULE + "api", "Test": "TestAuth", "Output": "want 403, got 200\n"},
            {"Action": "fail", "Package": ci.MODULE + "api", "Test": "TestAuth"},
            {"Action": "pass", "Package": ci.MODULE + "shared/auth", "Elapsed": 2.5}))
        self.assertEqual(len(failures), 1)
        self.assertIn("want 403", failures[0][2])
        self.assertEqual(timings, [("shared/auth", 2.5)])
        self.assertEqual(skips, [])

    def test_dependency_skips_fail_but_documented_previews_do_not(self):
        passed = {"Action": "pass", "Package": ci.MODULE + "worker", "Elapsed": 1}
        for package, test, want_failure in [
            ("worker/internal/worker", "TestSIPInvite_SIPpUAS_Integration/udp", True),
            ("worker/internal/worker", "TestSyntheticBrowserChecker_SuccessJourney", True),
            ("shared/queue", "TestClient_PublishAndConsume_Integration", True),
            ("status-page/internal/statuspage", "TestWritePreviewHTML", False)]:
            with self.subTest(test=test):
                failures, _, _ = ci.go_report(self.events(passed, {"Action": "skip", "Package": ci.MODULE + package, "Test": test}))
                self.assertEqual(bool(failures), want_failure)

    def test_summary_is_bounded_and_fences_hold(self):
        lines = ci.failure_summary([("api", "TestFence", "got ```\nwant ````")])
        self.assertIn("`````text", lines)
        many = [("api", f"Test{i}", "x" * 1000) for i in range(100)]
        lines = ci.failure_summary(many, limit=10_000)
        self.assertLess(sum(len(line) + 1 for line in lines), 12_000)
        self.assertIn("more failures are in the JSON log artifact", lines[-1])

    def test_empty_build_failure_and_raw_errors_are_not_success(self):
        self.assertTrue(ci.go_report([])[0])
        self.assertTrue(ci.go_report(self.events({"Action": "build-fail", "ImportPath": ci.MODULE + "api"}))[0])
        self.assertTrue(ci.go_report(["go: toolchain download failed"])[0])
        self.assertEqual(ci.annotation("bad%\nline\r"), "bad%25%0Aline%0D")


class CoverageTest(unittest.TestCase):
    def test_overlapping_counts_are_merged_without_losing_coverage(self):
        with tempfile.TemporaryDirectory() as directory:
            a, b = Path(directory) / "a.out", Path(directory) / "b.out"
            a.write_text("mode: atomic\nfile.go:1.1,2.1 1 2\nfile.go:3.1,4.1 1 0\n")
            b.write_text("mode: atomic\nfile.go:1.1,2.1 1 0\nfile.go:3.1,4.1 1 3\n")
            self.assertEqual(ci.merge_coverage([a, b]),
                             "mode: atomic\nfile.go:1.1,2.1 1 2\nfile.go:3.1,4.1 1 3\n")
            b.write_text("mode: set\nfile.go:1.1,2.1 1 1\n")
            with self.assertRaises(ValueError):
                ci.merge_coverage([a, b])

    def test_missing_profiles_only_allowed_after_a_failed_shard(self):
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaises(ValueError):
                ci.coverage_profiles(directory, allow_missing=True)
            (Path(directory) / "coverage-api.out").write_text("mode: atomic\n")
            with self.assertRaises(ValueError):
                ci.coverage_profiles(directory)
            self.assertEqual([p.name for p in ci.coverage_profiles(directory, allow_missing=True)],
                             ["coverage-api.out"])
            for shard in ci.SHARDS:
                (Path(directory) / f"coverage-{shard}.out").write_text("mode: atomic\n")
            self.assertEqual(len(ci.coverage_profiles(directory)), 3)


class RequiredResultsTest(unittest.TestCase):
    def results(self, decision="true"):
        results = {name: {"result": "success", "outputs": {}} for name in
                   ("changes", "go-format", "go-unit", "go-full", "ai-fast", "ai-checkpoint")}
        results["changes"]["outputs"]["ai_validation"] = decision
        return results

    def test_skipped_cpu_job_only_allowed_after_explicit_negative_decision(self):
        results = self.results("false")
        results["ai-checkpoint"]["result"] = "skipped"
        self.assertEqual(ci.required_failures(results), [])
        results["changes"]["outputs"]["ai_validation"] = "true"
        self.assertEqual(ci.required_failures(results), ["ai-checkpoint: skipped"])

    def test_failure_cancellation_skip_or_missing_result_is_not_green(self):
        self.assertEqual(ci.required_failures(self.results()), [])
        for state in ("failure", "cancelled", "skipped"):
            with self.subTest(state=state):
                results = self.results()
                results["go-full"]["result"] = state
                self.assertTrue(ci.required_failures(results))
        results = self.results()
        del results["ai-fast"]
        self.assertTrue(ci.required_failures(results))
        self.assertTrue(ci.required_failures(self.results("")))

    def test_each_stable_check_only_waits_for_its_own_suites(self):
        results = self.results("false")
        go = {name: results[name] for name in ("go-format", "go-unit", "go-full")}
        self.assertEqual(ci.required_failures(go, "go"), [])
        ai = {name: results[name] for name in ("changes", "ai-fast", "ai-checkpoint")}
        ai["ai-checkpoint"]["result"] = "skipped"
        self.assertEqual(ci.required_failures(ai, "ai"), [])
        ai["ai-fast"]["result"] = "failure"
        self.assertTrue(ci.required_failures(ai, "ai"))


class LayoutTest(unittest.TestCase):
    def test_integration_files_opt_out_of_unit_tag(self):
        root = Path(__file__).resolve().parents[3]
        files = subprocess.check_output(["git", "ls-files", "--cached", "--others", "--exclude-standard",
                                         "--", "*_integration_test.go"], cwd=root, text=True).splitlines()
        self.assertTrue(files)
        for name in files:
            with self.subTest(path=name):
                self.assertTrue((root / name).read_text(encoding="utf-8").startswith("//go:build !unit\n"))


if __name__ == "__main__":
    unittest.main()
