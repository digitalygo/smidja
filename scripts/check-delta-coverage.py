import re
import subprocess
import sys
from collections import defaultdict
from pathlib import Path

base, *profiles = sys.argv[1:]
diff = subprocess.check_output(["git", "diff", "--unified=0", base, "--", "*.go"], text=True)
changed = defaultdict(set)
path = None
for line in diff.splitlines():
    if line.startswith("+++ b/"):
        path = line[6:]
    elif line.startswith("@@") and path and not path.endswith("_test.go"):
        match = re.search(r"\+(\d+)(?:,(\d+))?", line)
        start = int(match[1])
        count = int(match[2] or 1)
        changed[path].update(range(start, start + count))
blocks = {}
profile_files = set()
module = subprocess.check_output(["go", "list", "-m"], text=True).strip() + "/"
for profile in profiles:
    for line in Path(profile).read_text().splitlines()[1:]:
        match = re.fullmatch(r"(.+):(\d+)\.(\d+),(\d+)\.(\d+) (\d+) (\d+)", line)
        if not match:
            raise SystemExit("Invalid coverage row")
        filename, start, _, end, _, statements, hits = match.groups()
        filename = filename.removeprefix(module)
        profile_files.add(filename)
        if any(number in changed.get(filename, ()) for number in range(int(start), int(end) + 1)):
            key = (filename, start, end, statements)
            blocks[key] = blocks.get(key, 0) + int(hits)
by_package = defaultdict(lambda: [0, 0])
for (filename, _, _, statements), hits in blocks.items():
    group = by_package[str(Path(filename).parent)]
    group[1] += int(statements)
    if hits:
        group[0] += int(statements)
if not by_package and changed:
    raise SystemExit("No executable changed-code coverage was measured")
failed = []
for package, (covered, total) in sorted(by_package.items()):
    ratio = covered / total if total else 1
    print(f"{package}: {covered}/{total} changed-block statements ({ratio:.1%})")
    if ratio < 0.8:
        failed.append(package)
missing = [path for path in changed if path.startswith("internal/") and path not in profile_files]
for path in missing:
    content = Path(path).read_text() if Path(path).exists() else ""
    if re.search(r"^func\s", content, flags=re.M):
        failed.append(path + " (no coverage blocks)")
if failed:
    raise SystemExit("Changed executable coverage below 80%: " + ", ".join(failed))
