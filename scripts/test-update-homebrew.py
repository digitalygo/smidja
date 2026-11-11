import base64
import contextlib
import hashlib
import importlib.util
import io
import json
import os
import sys
from pathlib import Path
import unittest
import urllib.error
import urllib.request
from unittest.mock import Mock, patch


SPEC = importlib.util.spec_from_file_location("homebrew", Path(__file__).with_name("update-homebrew.py"))
H = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(H)
COMMIT = "a" * 40
CHECKSUM = "b" * 64
TOKEN = "offline-secret-do-not-print"
TAG = "v0.4.0"


def formula(version="v0.3.0", checksum="c" * 64):
    return 'class Smidja < Formula\n  url "https://github.com/digitalygo/smidja/archive/refs/tags/' + version + '.tar.gz"\n  sha256 "' + checksum + '"\n  license "MIT"\nend\n'


def record(content):
    raw = content.encode() if isinstance(content, str) else content
    return {"type": "file", "encoding": "base64", "content": base64.b64encode(raw).decode(), "sha": hashlib.sha1(b"blob " + str(len(raw)).encode() + b"\0" + raw).hexdigest()}


class FakeAPI:
    def __init__(self, current=None):
        self.current = formula() if current is None else current
        self.release = {"tag_name": TAG, "draft": False, "prerelease": False}
        self.tags = [COMMIT, COMMIT]
        self.branch = "alpha"
        self.statuses = [200]
        self.requests = []
        self.gets = []
        self.on_conflict = None
        self.after_write = None

    def get(self, path):
        self.gets.append(path)
        if "/releases/tags/" in path:
            return self.release
        if "/git/ref/tags/" in path:
            sha = self.tags.pop(0) if len(self.tags) > 1 else self.tags[0]
            return {"object": {"type": "commit", "sha": sha}}
        if path == "/repos/digitalygo/homebrew-smidja":
            return {"default_branch": self.branch}
        if "/contents/Formula/smidja.rb?ref=" in path:
            return record(self.current)
        raise AssertionError(path)

    def request(self, method, path, payload=None):
        self.requests.append((method, path, payload))
        if isinstance(self.statuses[0], Exception):
            raise self.statuses.pop(0)
        status = self.statuses.pop(0) if len(self.statuses) > 1 else self.statuses[0]
        if status == 200:
            self.current = base64.b64decode(payload["content"]).decode()
            if self.after_write:
                self.current = self.after_write
        elif status == 409 and self.on_conflict:
            self.current = self.on_conflict
        return status, {}


class Response:
    def __init__(self, data=b"", status=200):
        self.stream = io.BytesIO(data)
        self.status = status

    def __enter__(self):
        return self

    def __exit__(self, *args):
        return False

    def read(self, size):
        return self.stream.read(size)


class VersionsAndFormulaTests(unittest.TestCase):
    def test_stable_tags_and_nonstable(self):
        self.assertEqual(H.stable_version("v0.0.0"), (0, 0, 0))
        self.assertEqual(H.stable_version("v12.34.56"), (12, 34, 56))
        for tag in ["v1.2.3-alpha.1", "v1.2.3+build.7", "v1.2.3-rc.1+build"]:
            with self.subTest(tag=tag):
                self.assertIsNone(H.stable_version(tag))
        for tag in ["1.2.3", "v01.2.3", "v1.2", "v1.2.3/evil", "v1.2.3\n", "v9999999999.0.0", ""]:
            with self.subTest(tag=tag), self.assertRaises(H.UpdateError):
                H.stable_version(tag)

    def test_only_url_and_checksum_change(self):
        before = formula()
        after = H.replacement(before, TAG, CHECKSUM)
        self.assertEqual(after, formula(TAG, CHECKSUM))
        self.assertIsNone(H.replacement(after, TAG, CHECKSUM))
        self.assertIsNone(H.replacement(formula("v1.0.0"), TAG, CHECKSUM))
        with self.assertRaisesRegex(H.UpdateError, "different checksum"):
            H.replacement(formula(TAG), TAG, CHECKSUM)

    def test_layout_rejected(self):
        for value in [formula().replace("  url", "url"), formula() + '  sha256 "' + CHECKSUM + '"\n', formula().replace("digitalygo/smidja/archive", "other/repo/archive"), formula().replace('  sha256 "', '  sha256 "XYZ')]:
            with self.subTest(value=value), self.assertRaisesRegex(H.UpdateError, "layout"):
                H.replacement(value, TAG, CHECKSUM)

    def test_formula_content_digest_and_shape(self):
        self.assertEqual(H.formula_content(record(formula())), formula())
        wrapped = record(formula())
        wrapped["content"] = "\n" + wrapped["content"] + "\n"
        self.assertEqual(H.formula_content(wrapped), formula())
        for key, value in [("type", "symlink"), ("encoding", "utf-8"), ("sha", "0" * 40)]:
            bad = record(formula())
            bad[key] = value
            with self.subTest(key=key), self.assertRaises(H.UpdateError):
                H.formula_content(bad)
        with self.assertRaisesRegex(H.UpdateError, "limit"):
            H.formula_content(record(b"x" * ((1 << 20) + 1)))
        with self.assertRaises(Exception):
            H.formula_content({"type": "file", "encoding": "base64", "content": "!", "sha": "x"})
        with self.assertRaises(UnicodeError):
            H.formula_content(record(b"\xff"))


class UpdateTests(unittest.TestCase):
    def update(self, api, tag=TAG, commit=COMMIT, checksum=CHECKSUM):
        calls = []
        result = H.update(api, tag, commit, lambda url: calls.append(url) or checksum)
        return result, calls

    def test_success_exact_source_bytes_and_optimistic_binding(self):
        api = FakeAPI()
        old = record(api.current)["sha"]
        result, downloads = self.update(api)
        self.assertIn("updated and verified", result)
        self.assertEqual(downloads, ["https://github.com/digitalygo/smidja/archive/refs/tags/v0.4.0.tar.gz"])
        self.assertEqual(len(api.requests), 1)
        method, path, payload = api.requests[0]
        self.assertEqual(method, "PUT")
        self.assertEqual(path, "/repos/digitalygo/homebrew-smidja/contents/Formula/smidja.rb")
        self.assertEqual(set(payload), {"message", "content", "sha", "branch"})
        self.assertEqual(payload["sha"], old)
        self.assertEqual(payload["branch"], "alpha")
        self.assertEqual(api.current, formula(TAG, CHECKSUM))

    def test_nonstable_skips_all_remote_work(self):
        for tag in ["v0.4.0-alpha.1", "v0.4.0+build"]:
            api = FakeAPI()
            result, downloads = self.update(api, tag)
            self.assertIn("Skipped", result)
            self.assertEqual(api.gets, [])
            self.assertEqual(api.requests, [])
            self.assertEqual(downloads, [])

    def test_invalid_commit_and_release_before_download_or_write(self):
        for commit in ["", "x" * 40, "a" * 41]:
            with self.subTest(commit=commit), self.assertRaisesRegex(H.UpdateError, "commit"):
                self.update(FakeAPI(), commit=commit)
        for key, value in [("draft", True), ("prerelease", True), ("tag_name", "v0.5.0"), ("draft", None)]:
            api = FakeAPI()
            api.release[key] = value
            download = Mock()
            with self.subTest(key=key), self.assertRaisesRegex(H.UpdateError, "published stable"):
                H.update(api, TAG, COMMIT, download)
            download.assert_not_called()
            self.assertEqual(api.requests, [])

    def test_tag_changed_before_or_after_archive(self):
        for tags, expected_downloads in [(["d" * 40], 0), ([COMMIT, "d" * 40], 1)]:
            api = FakeAPI()
            api.tags = tags
            download = Mock(return_value=CHECKSUM)
            with self.assertRaisesRegex(H.UpdateError, "tag"):
                H.update(api, TAG, COMMIT, download)
            self.assertEqual(download.call_count, expected_downloads)
            self.assertEqual(api.requests, [])

    def test_invalid_checksum_and_branch(self):
        for checksum in ["", "z" * 64, "b" * 63]:
            with self.subTest(checksum=checksum), self.assertRaisesRegex(H.UpdateError, "checksum"):
                self.update(FakeAPI(), checksum=checksum)
        for branch in ["", None, 123, "x" * 257]:
            api = FakeAPI()
            api.branch = branch
            with self.subTest(branch=branch), self.assertRaisesRegex(H.UpdateError, "branch"):
                self.update(api)
            self.assertEqual(api.requests, [])

    def test_branch_query_is_encoded(self):
        api = FakeAPI()
        api.branch = "release/stable"
        self.update(api)
        self.assertTrue(any(path.endswith("?ref=release%2Fstable") for path in api.gets))
        self.assertEqual(api.requests[0][2]["branch"], "release/stable")

    def test_same_or_newer_version_no_write(self):
        for current in [formula(TAG, CHECKSUM), formula("v1.0.0")]:
            api = FakeAPI(current)
            result, _ = self.update(api)
            self.assertIn("no change", result)
            self.assertEqual(api.requests, [])
        api = FakeAPI(formula(TAG))
        with self.assertRaisesRegex(H.UpdateError, "mutable"):
            self.update(api)
        self.assertEqual(api.requests, [])

    def test_conflict_refetch_preserves_newer_release(self):
        api = FakeAPI()
        api.statuses = [409]
        api.on_conflict = formula("v0.5.0")
        result, _ = self.update(api)
        self.assertIn("newer", result)
        self.assertEqual(len(api.requests), 1)
        self.assertEqual(api.current, formula("v0.5.0"))

    def test_conflict_refetch_new_sha_then_success(self):
        api = FakeAPI()
        api.statuses = [409, 200]
        api.on_conflict = formula("v0.3.1")
        self.update(api)
        self.assertEqual(len(api.requests), 2)
        self.assertNotEqual(api.requests[0][2]["sha"], api.requests[1][2]["sha"])
        self.assertEqual(api.current, formula(TAG, CHECKSUM))

    def test_conflict_bound_and_protection_fail_closed(self):
        api = FakeAPI()
        api.statuses = [409]
        with self.assertRaisesRegex(H.UpdateError, "three times"):
            self.update(api)
        self.assertEqual(len(api.requests), 3)
        for status in [401, 403, 422, 500]:
            api = FakeAPI()
            api.statuses = [status]
            with self.subTest(status=status), self.assertRaisesRegex(H.UpdateError, "no bypass"):
                self.update(api)
            self.assertEqual(len(api.requests), 1)

    def test_uncertain_write_is_not_retried(self):
        api = FakeAPI()
        api.statuses = [H.UpdateError("transport failure")]
        with self.assertRaisesRegex(H.UpdateError, "transport"):
            self.update(api)
        self.assertEqual(len(api.requests), 1)

    def test_readback_accepts_exact_or_newer_rejects_stale_or_rewrite(self):
        api = FakeAPI()
        api.after_write = formula("v1.0.0")
        self.update(api)
        for current in [formula(), formula(TAG)]:
            api = FakeAPI()
            api.after_write = current
            with self.subTest(current=current), self.assertRaises(H.UpdateError):
                self.update(api)
            self.assertEqual(len(api.requests), 1)

    def test_annotated_tag_resolution_and_rejections(self):
        api = Mock()
        api.get.side_effect = [{"object": {"type": "tag", "sha": "d" * 40}}, {"object": {"type": "commit", "sha": COMMIT}}]
        self.assertEqual(H.resolve_tag(api, TAG), COMMIT)
        self.assertEqual(api.get.call_args_list[-1].args[0], "/repos/digitalygo/smidja/git/tags/" + "d" * 40)
        for obj in [{"type": "tree", "sha": COMMIT}, {"type": "commit", "sha": "bad"}]:
            api = Mock()
            api.get.return_value = {"object": obj}
            with self.assertRaises(H.UpdateError):
                H.resolve_tag(api, TAG)
        api = Mock()
        api.get.return_value = {"object": {"type": "tag", "sha": "d" * 40}}
        with self.assertRaisesRegex(H.UpdateError, "chain"):
            H.resolve_tag(api, TAG)
        self.assertEqual(api.get.call_count, 5)


class TransportTests(unittest.TestCase):
    def test_api_headers_payload_and_no_redirects(self):
        with patch.object(H.urllib.request, "build_opener") as build:
            build.return_value.open.return_value = Response(b'{"ok":true}')
            api = H.GitHub(TOKEN)
            status, data = api.request("PUT", "/repos/digitalygo/homebrew-smidja/contents/Formula/smidja.rb", {"test": "value"})
            self.assertEqual((status, data), (200, {"ok": True}))
            build.return_value.open.return_value = Response(b'{"ok":true}')
            self.assertEqual(api.get("/repos/digitalygo/smidja"), {"ok": True})
            request = build.return_value.open.call_args_list[0].args[0]
            self.assertTrue(request.full_url.startswith("https://api.github.com/"))
            self.assertEqual(request.get_header("Authorization"), "Bearer " + TOKEN)
            self.assertEqual(json.loads(request.data), {"test": "value"})
            self.assertIsInstance(build.call_args.args[0], H.NoRedirects)
            self.assertIsNone(H.NoRedirects().redirect_request(None, None, 302, "", {}, "https://evil.invalid"))

    def test_api_http_status_not_body_and_read_error(self):
        with patch.object(H.urllib.request, "build_opener") as build:
            build.return_value.open.side_effect = urllib.error.HTTPError("https://api.github.com/test", 403, TOKEN, {}, io.BytesIO(TOKEN.encode()))
            api = H.GitHub(TOKEN)
            self.assertEqual(api.request("PUT", "/test"), (403, None))
            with self.assertRaisesRegex(H.UpdateError, "HTTP 403") as caught:
                api.get("/test")
            self.assertNotIn(TOKEN, str(caught.exception))

    def test_api_transport_and_bad_json_redacted(self):
        for effect in [OSError(TOKEN), ValueError(TOKEN)]:
            with patch.object(H.urllib.request, "build_opener") as build:
                build.return_value.open.side_effect = effect
                with self.assertRaises(H.UpdateError) as caught:
                    H.GitHub(TOKEN).request("PUT", "/test")
                self.assertNotIn(TOKEN, str(caught.exception))
                self.assertEqual(build.return_value.open.call_count, 1)
        for raw in [TOKEN.encode(), b"x" * ((2 << 20) + 1)]:
            with patch.object(H.urllib.request, "build_opener") as build:
                build.return_value.open.return_value = Response(raw)
                with self.assertRaises(H.UpdateError):
                    H.GitHub(TOKEN).request("GET", "/test")
        with patch.object(H.urllib.request, "build_opener") as build:
            build.return_value.open.return_value = Response(b"", 204)
            self.assertEqual(H.GitHub(TOKEN).request("DELETE", "/test"), (204, None))

    def test_archive_hashes_downloaded_bytes_without_token(self):
        data = b"source tar bytes, not release binary checksum"
        with patch.object(H.urllib.request, "build_opener") as build:
            build.return_value.open.return_value = Response(data)
            self.assertEqual(H.archive_digest("https://github.com/digitalygo/smidja/archive/refs/tags/v0.4.0.tar.gz"), hashlib.sha256(data).hexdigest())
            request = build.return_value.open.call_args.args[0]
            self.assertIsNone(request.get_header("Authorization"))
            self.assertIsInstance(build.call_args.args[0], H.ArchiveRedirects)

    def test_archive_empty_bad_status_transport_and_bound(self):
        for response in [Response(), Response(b"no", 500)]:
            with patch.object(H.urllib.request, "build_opener") as build:
                build.return_value.open.return_value = response
                with self.assertRaises(H.UpdateError):
                    H.archive_digest("https://github.com/test")
        with patch.object(H.urllib.request, "build_opener") as build:
            build.return_value.open.side_effect = OSError(TOKEN)
            with self.assertRaises(H.UpdateError) as caught:
                H.archive_digest("https://github.com/test")
            self.assertNotIn(TOKEN, str(caught.exception))
        response = Response()
        response.read = Mock(return_value=b"x" * (1 << 20))
        with patch.object(H.urllib.request, "build_opener") as build:
            build.return_value.open.return_value = response
            with self.assertRaises(H.UpdateError):
                H.archive_digest("https://github.com/test")
            self.assertEqual(response.read.call_count, 257)

    def test_archive_redirect_allowlist(self):
        redirect = H.ArchiveRedirects()
        request = urllib.request.Request("https://github.com/source")
        for target in ["https://codeload.github.com/digitalygo/smidja/tar.gz/refs/tags/v0.4.0", "https://github.com/source"]:
            self.assertEqual(redirect.redirect_request(request, None, 302, "", {}, target).full_url, target)
        for target in ["http://codeload.github.com/source", "https://evil.invalid/source", "https://github.com.evil.invalid/source", "https://user@github.com/source", "https://github.com:444/source"]:
            with self.subTest(target=target), self.assertRaises(H.UpdateError):
                redirect.redirect_request(request, None, 302, "", {}, target)


class MainTests(unittest.TestCase):
    def invoke(self, changes=None, result="ok", error=None):
        environment = {"GITHUB_REPOSITORY": H.SOURCE, "GITHUB_EVENT_NAME": "push", "GITHUB_REF_TYPE": "tag", "GITHUB_REF_NAME": TAG, "RELEASE_TOKEN": TOKEN, "BUILT_COMMIT": COMMIT}
        environment.update(changes or {})
        output, errors = io.StringIO(), io.StringIO()
        with patch.dict(os.environ, environment, clear=True), patch.object(H, "GitHub") as api, patch.object(H, "update", return_value=result, side_effect=error) as update, contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
            code = H.main()
        text = output.getvalue() + errors.getvalue()
        self.assertNotIn(TOKEN, text)
        return code, text, api, update

    def test_main_success_and_source_guards(self):
        code, text, api, update = self.invoke()
        self.assertEqual(code, 0)
        api.assert_called_once_with(TOKEN)
        self.assertEqual(update.call_args.args[1:], (TAG, COMMIT))
        for changes in [{"GITHUB_REPOSITORY": "fork/smidja"}, {"GITHUB_EVENT_NAME": "pull_request"}, {"GITHUB_REF_TYPE": "branch"}]:
            code, text, api, update = self.invoke(changes)
            self.assertEqual(code, 1)
            api.assert_not_called()
            update.assert_not_called()

    def test_main_skip_missing_token_and_redaction(self):
        code, text, api, update = self.invoke({"GITHUB_REF_NAME": "v0.4.0-alpha.1", "RELEASE_TOKEN": ""})
        self.assertEqual(code, 0)
        api.assert_not_called()
        code, text, api, update = self.invoke({"RELEASE_TOKEN": ""})
        self.assertEqual(code, 1)
        self.assertIn("unavailable", text)
        api.assert_not_called()
        code, text, _, _ = self.invoke(error=ValueError(TOKEN))
        self.assertEqual(code, 1)
        self.assertIn("Invalid release", text)
        code, text, _, _ = self.invoke(error=H.UpdateError("safe diagnostic"))
        self.assertEqual(code, 1)
        self.assertIn("safe diagnostic", text)


class WorkflowTests(unittest.TestCase):
    def test_release_runs_updater_only_after_successful_publish(self):
        root = Path(__file__).resolve().parents[1]
        workflow = (root / ".github/workflows/release.yml").read_text()
        self.assertIn('on:\n  push:\n    tags:\n      - "v*"', workflow)
        self.assertNotIn("pull_request", workflow)
        self.assertNotIn("workflow_dispatch", workflow)
        self.assertNotIn("concurrency:", workflow)
        self.assertNotIn("cancel-in-progress", workflow)
        self.assertNotIn("continue-on-error", workflow)
        self.assertNotIn("always()", workflow)
        self.assertLess(workflow.index("- name: Publish release"), workflow.index("- name: Update Homebrew stable formula"))
        before, updater = workflow.split("      - name: Update Homebrew stable formula", 1)
        self.assertNotIn("secrets.RELEASE_TOKEN", before)
        self.assertEqual(updater.count("${{ secrets.RELEASE_TOKEN }}"), 1)
        self.assertIn("        env:\n          RELEASE_TOKEN: ${{ secrets.RELEASE_TOKEN }}", updater)
        self.assertIn("github.repository == 'digitalygo/smidja' && github.event_name == 'push' && github.ref_type == 'tag'", updater)
        self.assertIn('export BUILT_COMMIT="$(git rev-parse HEAD)"', updater)
        self.assertIn("python3 scripts/update-homebrew.py", updater)
        self.assertIn("persist-credentials: false", before)
        self.assertIn('RELEASE_TAG: ${{ github.ref_name }}', before)
        self.assertIn('run: ./scripts/build-release.sh "$RELEASE_TAG" dist', before)
        for line in workflow.splitlines():
            if line.lstrip().startswith("run:"):
                self.assertNotIn("${{", line)

    def test_offline_workflow_cannot_publish(self):
        root = Path(__file__).resolve().parents[1]
        workflow = (root / ".github/workflows/homebrew-automation.yml").read_text()
        self.assertIn("pull_request:", workflow)
        self.assertIn("branches: [feature/smidja-homebrew-release]", workflow)
        self.assertIn("permissions:\n  contents: read", workflow)
        self.assertIn("persist-credentials: false", workflow)
        self.assertIn("python3 scripts/test-update-homebrew.py", workflow)
        self.assertIn("python3 -m trace", workflow)
        self.assertNotIn("secrets.", workflow)
        self.assertNotIn("contents: write", workflow)
        self.assertNotIn("python3 scripts/update-homebrew.py", workflow)
        self.assertNotIn("workflow_run", workflow)


if __name__ == "__main__":
    suite = unittest.TestSuite(unittest.TestLoader().loadTestsFromTestCase(case) for case in (VersionsAndFormulaTests, UpdateTests, TransportTests, MainTests, WorkflowTests))
    outcome = unittest.TextTestRunner().run(suite)
    sys.exit(0 if outcome.wasSuccessful() else 1)
