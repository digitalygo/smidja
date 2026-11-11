import base64
import hashlib
import json
import os
import re
import sys
import urllib.error
import urllib.parse
import urllib.request


SOURCE = "digitalygo/smidja"
TAP = "digitalygo/homebrew-smidja"
FORMULA = "Formula/smidja.rb"
STABLE = re.compile(r"v(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})")
URL_LINE = re.compile(r'^  url "https://github\.com/digitalygo/smidja/archive/refs/tags/(v[0-9]+\.[0-9]+\.[0-9]+)\.tar\.gz"$', re.M)
SHA_LINE = re.compile(r'^  sha256 "([0-9a-f]{64})"$', re.M)


class UpdateError(Exception):
    pass


class NoRedirects(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class ArchiveRedirects(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        target = urllib.parse.urlparse(newurl)
        if target.scheme != "https" or target.hostname not in {"github.com", "codeload.github.com"} or target.username or target.password or target.port not in (None, 443):
            raise UpdateError("source archive redirect was rejected")
        return super().redirect_request(req, fp, code, msg, headers, newurl)


class GitHub:
    def __init__(self, token):
        self.token = token
        self.opener = urllib.request.build_opener(NoRedirects())

    def request(self, method, path, payload=None):
        body = None if payload is None else json.dumps(payload).encode()
        request = urllib.request.Request("https://api.github.com" + path, data=body, method=method, headers={
            "Authorization": "Bearer " + self.token,
            "Accept": "application/vnd.github+json",
            "X-GitHub-Api-Version": "2022-11-28",
            "Content-Type": "application/json",
            "User-Agent": "smidja-homebrew-release",
        })
        try:
            with self.opener.open(request, timeout=30) as response:
                data = response.read((2 << 20) + 1)
                if len(data) > 2 << 20:
                    raise UpdateError("GitHub response exceeded limit")
                return response.status, json.loads(data) if data else None
        except urllib.error.HTTPError as error:
            status = error.code
            error.close()
            return status, None
        except Exception:
            raise UpdateError("GitHub transport or protocol failure; inspect the tap before retrying") from None

    def get(self, path):
        status, value = self.request("GET", path)
        if status != 200:
            raise UpdateError("GitHub read failed with HTTP " + str(status))
        return value


def stable_version(tag):
    match = STABLE.fullmatch(tag)
    if match:
        return tuple(int(part) for part in match.groups())
    if re.fullmatch(STABLE.pattern + r"(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?", tag):
        return None
    raise UpdateError("invalid release tag")


def archive_digest(url):
    opener = urllib.request.build_opener(ArchiveRedirects())
    digest = hashlib.sha256()
    total = 0
    try:
        with opener.open(urllib.request.Request(url, headers={"User-Agent": "smidja-homebrew-release"}), timeout=60) as response:
            if response.status != 200:
                raise UpdateError("source archive download failed")
            while chunk := response.read(1 << 20):
                total += len(chunk)
                if total > 256 << 20:
                    raise UpdateError("source archive exceeded 256 MiB")
                digest.update(chunk)
    except Exception:
        raise UpdateError("source archive download failed; no tap change was attempted") from None
    if total == 0:
        raise UpdateError("source archive is empty")
    return digest.hexdigest()


def resolve_tag(api, tag):
    value = api.get("/repos/" + SOURCE + "/git/ref/tags/" + tag)["object"]
    for _ in range(4):
        sha = value.get("sha", "")
        if not re.fullmatch(r"[0-9a-f]{40}", sha):
            raise UpdateError("invalid release tag object")
        if value.get("type") == "commit":
            return sha
        if value.get("type") != "tag":
            raise UpdateError("release tag does not resolve to a commit")
        value = api.get("/repos/" + SOURCE + "/git/tags/" + sha)["object"]
    raise UpdateError("release tag chain exceeded limit")


def formula_content(record):
    if record.get("type") != "file" or record.get("encoding") != "base64":
        raise UpdateError("tap formula is not a base64 file")
    raw = base64.b64decode("".join(record["content"].split()), validate=True)
    if len(raw) > 1 << 20:
        raise UpdateError("tap formula exceeded limit")
    sha = hashlib.sha1(b"blob " + str(len(raw)).encode() + b"\0" + raw).hexdigest()
    if sha != record.get("sha"):
        raise UpdateError("tap formula content identity mismatch")
    return raw.decode("utf-8")


def replacement(formula, tag, checksum):
    urls, checksums = list(URL_LINE.finditer(formula)), list(SHA_LINE.finditer(formula))
    if len(urls) != 1 or len(checksums) != 1:
        raise UpdateError("tap formula layout changed; manual review required")
    current = stable_version(urls[0].group(1))
    wanted = stable_version(tag)
    if current > wanted:
        return None
    if current == wanted:
        if checksums[0].group(1) != checksum:
            raise UpdateError("same version has a different checksum; refusing mutable release rewrite")
        return None
    updated = URL_LINE.sub('  url "https://github.com/' + SOURCE + '/archive/refs/tags/' + tag + '.tar.gz"', formula)
    return SHA_LINE.sub('  sha256 "' + checksum + '"', updated)


def update(api, tag, commit, download=archive_digest):
    if stable_version(tag) is None:
        return "Skipped prerelease or build-metadata tag; stable Homebrew formula unchanged"
    if not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise UpdateError("invalid source commit")
    release = api.get("/repos/" + SOURCE + "/releases/tags/" + tag)
    if release.get("tag_name") != tag or release.get("draft") is not False or release.get("prerelease") is not False:
        raise UpdateError("a published stable release is required")
    if resolve_tag(api, tag) != commit:
        raise UpdateError("release tag no longer matches the built commit")
    url = "https://github.com/" + SOURCE + "/archive/refs/tags/" + tag + ".tar.gz"
    checksum = download(url)
    if not re.fullmatch(r"[0-9a-f]{64}", checksum):
        raise UpdateError("invalid source archive checksum")
    if resolve_tag(api, tag) != commit:
        raise UpdateError("release tag changed during archive download")
    branch = api.get("/repos/" + TAP)["default_branch"]
    if not isinstance(branch, str) or not branch or len(branch) > 256:
        raise UpdateError("invalid tap default branch")
    path = "/repos/" + TAP + "/contents/" + FORMULA
    query = "?ref=" + urllib.parse.quote(branch, safe="")
    for _ in range(3):
        record = api.get(path + query)
        formula = formula_content(record)
        changed = replacement(formula, tag, checksum)
        if changed is None:
            return "Tap already contains this exact release or a newer version; no change"
        status, _ = api.request("PUT", path, {
            "message": "chore: update Smidja to " + tag,
            "content": base64.b64encode(changed.encode()).decode(),
            "sha": record["sha"],
            "branch": branch,
        })
        if status == 409:
            continue
        if status != 200:
            raise UpdateError("tap update rejected with HTTP " + str(status) + "; check RELEASE_TOKEN access and branch protection; no bypass attempted")
        saved = formula_content(api.get(path + query))
        if replacement(saved, tag, checksum) is not None:
            raise UpdateError("tap update could not be verified; inspect before retrying")
        return "Homebrew formula updated and verified for " + tag
    raise UpdateError("tap changed concurrently three times; inspect before retrying")


def main():
    try:
        if os.environ.get("GITHUB_REPOSITORY") != SOURCE or os.environ.get("GITHUB_EVENT_NAME") != "push" or os.environ.get("GITHUB_REF_TYPE") != "tag":
            raise UpdateError("Homebrew updates require a source-repository tag push")
        tag = os.environ.get("GITHUB_REF_NAME", "")
        if stable_version(tag) is None:
            print("Skipped non-stable tag; Homebrew unchanged")
            return 0
        token = os.environ.get("RELEASE_TOKEN", "")
        if not token:
            raise UpdateError("RELEASE_TOKEN is unavailable; release exists but Homebrew was not updated")
        print(update(GitHub(token), tag, os.environ.get("BUILT_COMMIT", "")))
        return 0
    except Exception as error:
        print(str(error) if isinstance(error, UpdateError) else "Invalid release or tap metadata; inspect before retrying", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
