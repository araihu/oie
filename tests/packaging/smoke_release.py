#!/usr/bin/env python3
"""Install and run a real production Linux archive with disposable user data."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import sqlite3
import struct
import subprocess
import tempfile
import time
import zlib


def require(condition, message):
    if not condition:
        raise AssertionError(message)


def wait_for(check, process, timeout=45):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        require(process.poll() is None, f"app exited during startup: {process.returncode}")
        result = check()
        if result:
            return result
        time.sleep(0.2)
    raise AssertionError("production application did not become ready")


def identity(database):
    with sqlite3.connect(database) as connection:
        require(connection.execute("PRAGMA integrity_check").fetchone()[0] == "ok", "SQLite integrity check failed")
        return connection.execute("SELECT id FROM engine_identity").fetchone()[0]


def visible_window():
    result = subprocess.run(["xdotool", "search", "--onlyvisible", "--name", "^Oiê$"], capture_output=True, text=True, check=False)
    return result.stdout.strip().splitlines()[-1] if result.returncode == 0 else None


def painted_frame(path):
    subprocess.run(["scrot", "--overwrite", str(path)], check=True)
    data = path.read_bytes()
    require(data[:8] == b"\x89PNG\r\n\x1a\n", "screen capture is not PNG")
    width, height, depth, kind, _, _, interlace = struct.unpack(">IIBBBBB", data[16:29])
    require(depth == 8 and kind in (2, 6) and interlace == 0, "unexpected screenshot PNG format")
    offset, compressed = 8, []
    while offset + 12 <= len(data):
        size = struct.unpack(">I", data[offset:offset + 4])[0]
        if data[offset + 4:offset + 8] == b"IDAT":
            compressed.append(data[offset + 8:offset + 8 + size])
        offset += size + 12
    # A native menu can paint before HTML and CSS. Require the approved blue
    # brand in the upper app viewport, not merely a mapped/white GTK window.
    raw = zlib.decompress(b"".join(compressed))
    channels = 3 if kind == 2 else 4
    stride = width * channels
    previous = bytearray(stride)
    matches = 0
    for y in range(min(height, 320)):
        position = y * (stride + 1)
        mode = raw[position]
        row = bytearray(raw[position + 1:position + 1 + stride])
        for x in range(stride):
            left = row[x - channels] if x >= channels else 0
            up = previous[x]
            corner = previous[x - channels] if x >= channels else 0
            if mode == 1:
                predictor = left
            elif mode == 2:
                predictor = up
            elif mode == 3:
                predictor = (left + up) // 2
            elif mode == 4:
                value = left + up - corner
                distances = (abs(value - left), abs(value - up), abs(value - corner))
                predictor = (left, up, corner)[distances.index(min(distances))]
            else:
                require(mode == 0, "invalid PNG filter")
                predictor = 0
            row[x] = (row[x] + predictor) & 255
        for x in range(0, stride, channels):
            if abs(row[x] - 96) <= 8 and abs(row[x + 1] - 146) <= 8 and abs(row[x + 2] - 171) <= 8:
                matches += 1
                if matches >= 32:
                    return True
        previous = row
    return False


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--archive", required=True, type=Path)
    parser.add_argument("--installer", required=True, type=Path)
    parser.add_argument("--version", required=True)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--artifacts", required=True, type=Path)
    args = parser.parse_args()
    args.artifacts.mkdir(parents=True, exist_ok=True)
    report = {"platform": "linux/amd64", "version": args.version, "revision": args.revision, "checks": [], "status": "failed"}
    try:
        require(os.uname().machine == "x86_64", "runtime validation must run on Linux x86_64")
        require(shutil.which("go") is None, "runtime-only container unexpectedly has Go")
        require(shutil.which("gcc") is None, "runtime-only container unexpectedly has a C compiler")
        with tempfile.TemporaryDirectory(prefix="oie-packaged-runtime-") as task:
            root = Path(task)
            home = root / "test user"
            home.mkdir()
            assets = root / "assets"
            assets.mkdir()
            shutil.copy2(args.archive, assets / args.archive.name)
            checksum = hashlib.sha256(args.archive.read_bytes()).hexdigest()
            (assets / "SHA256SUMS").write_text(f"{checksum}  {args.archive.name}\n")
            commands = root / "commands"
            commands.mkdir()
            # Serve the real release bytes through the exact GitHub download URL
            # contract without making this runtime test depend on publication.
            curl = commands / "curl"
            curl.write_text('''#!/usr/bin/python3
import os
from pathlib import Path
import shutil
import sys
from urllib.parse import urlsplit
arguments = sys.argv[1:]
url = next(value for value in arguments if value.startswith("https://"))
parts = urlsplit(url)
base = "/araihu/oie/releases/download/" + os.environ["OIE_TEST_VERSION"] + "/"
if parts.netloc != "github.com" or not parts.path.startswith(base):
    raise SystemExit("unexpected release URL: " + url)
name = parts.path[len(base):]
if name not in ("SHA256SUMS", os.environ["OIE_TEST_ARCHIVE"]):
    raise SystemExit("unexpected release asset: " + name)
output = arguments[arguments.index("-o") + 1] if "-o" in arguments else arguments[arguments.index("--output") + 1]
shutil.copyfile(Path(os.environ["OIE_TEST_ASSETS"]) / name, output)
''')
            curl.chmod(0o755)
            env = os.environ.copy()
            for name in ("XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "SUDO_USER"):
                env.pop(name, None)
            env.update(HOME=str(home), PATH=str(commands) + ":" + os.environ["PATH"],
                       OIE_TEST_ASSETS=str(assets), OIE_TEST_VERSION=args.version, OIE_TEST_ARCHIVE=args.archive.name)
            command = ["sh", str(args.installer), "--version", args.version]
            installed = subprocess.run(command, env=env, capture_output=True, text=True, check=False)
            (args.artifacts / "install.log").write_text(installed.stdout + installed.stderr)
            require(installed.returncode == 0, "production archive installation failed: " + installed.stderr)
            executable = home / ".local/bin/oie-desktop"
            header = executable.read_bytes()[:20]
            require(header[:4] == b"\x7fELF" and int.from_bytes(header[18:20], "little") == 62, "installed executable is not ELF amd64")
            require("not found" not in subprocess.check_output(["ldd", str(executable)], text=True), "installed app has missing runtime libraries")
            report["checks"].append("installed actual amd64 archive with runtime libraries only and HOME containing spaces")
            profile = home / ".local/share/oie"
            database = profile / "oie.sqlite"
            process = None
            try:
                with (args.artifacts / "app.log").open("w") as log:
                    process = subprocess.Popen([str(executable), "--data-dir", str(profile)], env=env, stdout=log, stderr=subprocess.STDOUT)
                    window = wait_for(visible_window, process)
                    wait_for(lambda: database.exists() and database.stat().st_size > 0, process)
                    first_identity = identity(database)
                    wait_for(lambda: painted_frame(args.artifacts / "installed-app.png"), process)
                    report["checks"].append("production WebView window painted and embedded SQLite initialized")
                    subprocess.run(["xdotool", "windowfocus", "--sync", window], check=True)
                    subprocess.run(["xdotool", "key", "--clearmodifiers", "ctrl+q"], check=True)
                    require(process.wait(timeout=20) == 0, "Ctrl+Q did not quit cleanly")
                    process = None
                require(identity(database) == first_identity, "engine identity changed on Quit")
                marker = profile / "packaging-preserve.txt"
                marker.write_text("retained user data\n")
                updated = subprocess.run(command, env=env, capture_output=True, text=True, check=False)
                (args.artifacts / "update.log").write_text(updated.stdout + updated.stderr)
                require(updated.returncode == 0, "production archive update failed: " + updated.stderr)
                require(marker.read_text() == "retained user data\n", "update lost user data")
                require(identity(database) == first_identity, "update replaced the user's engine database")
                report["checks"].append("explicit Quit and repeated installation preserve SQLite identity and user files")
            finally:
                if process is not None and process.poll() is None:
                    process.terminate()
                    try:
                        process.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait(timeout=5)
            report["status"] = "passed"
    finally:
        (args.artifacts / "report.json").write_text(json.dumps(report, indent=2) + "\n")


if __name__ == "__main__":
    main()
