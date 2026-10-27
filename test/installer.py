"""Exercise the installer with packaged binaries and local download fixtures."""
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

repo = Path(__file__).resolve().parents[1]
assets = Path(sys.argv[1]).resolve()
version = sys.argv[2]

with tempfile.TemporaryDirectory(prefix="slack-hours-installer-") as directory:
    root = Path(directory)
    fixtures = root / "assets"
    shutil.copytree(assets, fixtures)
    mocks = root / "commands"
    mocks.mkdir()
    curl = mocks / "curl"
    curl.write_text(f"#!{sys.executable}\n" + '''import os, pathlib, shutil, sys
args = sys.argv[1:]
if '--write-out' in args:
    print('https://github.com/raine/slack-hours/releases/tag/' + os.environ['TEST_VERSION'], end='')
else:
    source = pathlib.Path(os.environ['TEST_ASSETS']) / args[-1].split('/')[-1]
    shutil.copyfile(source, args[args.index('--output') + 1])
''')
    curl.chmod(0o755)
    env = {**os.environ, "PATH": str(mocks) + os.pathsep + os.environ["PATH"],
           "TEST_VERSION": version, "TEST_ASSETS": str(fixtures),
           "SLACK_HOURS_PREFIX": str(root / "install")}
    env.pop("SLACK_HOURS_VERSION", None)

    def run(success):
        result = subprocess.run([str(repo / "scripts/install")], env=env,
                                text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        if (result.returncode == 0) != success:
            raise AssertionError(result.stdout)
        return result.stdout

    run(True)  # latest-release resolution
    binary = root / "install/lib/slack-hours/slack-hours"
    link = root / "install/bin/slack-hours"
    assert link.is_symlink() and link.resolve() == binary.resolve()
    assert subprocess.check_output([str(link), "version"], text=True).strip() == "slack-hours " + version[1:]
    original = hashlib.sha256(binary.read_bytes()).hexdigest()
    env["SLACK_HOURS_VERSION"] = version
    run(True)  # upgrading an existing installation
    assert hashlib.sha256(binary.read_bytes()).hexdigest() == original
    link.unlink()
    link.write_text("unrelated executable")
    assert "refusing to replace" in run(False)
    assert link.read_text() == "unrelated executable"
    link.unlink()
    link.symlink_to(binary)
    for archive in fixtures.glob("*.tar.gz"):
        with archive.open("ab") as stream:
            stream.write(b"corrupted")
    assert "checksum verification failed" in run(False)
    assert hashlib.sha256(binary.read_bytes()).hexdigest() == original
    env["SLACK_HOURS_VERSION"] = "../../invalid"
    run(False)
    env.pop("SLACK_HOURS_VERSION")
    env["TEST_VERSION"] = "releases"
    assert "no binary release is available" in run(False)
    print("Installer: latest release, pinned version, upgrade, conflict and corruption checks passed.")
