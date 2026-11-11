"""Functional release-installer tests. Every download and user directory is fake."""
import hashlib
import io
import json
import os
from pathlib import Path
import plistlib
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
import unittest
import zlib
import struct

ROOT = Path(__file__).resolve().parents[2]
INSTALLER = ROOT / "scripts/install.sh"
VERSION = "v0.0.1-rc.0"
RELEASES = "https://github.com/araihu/oie/releases/download/"
EXECUTABLE = b"#!/bin/sh\nprintf '%s\\n' 'Oie installer fixture one'\n"
DESKTOP = b"[Desktop Entry]\nType=Application\nName=Oi\xc3\xaa\nExec=oie-desktop\nIcon=org.araihu.oie\nTerminal=false\nCategories=Network;InstantMessaging;\n"


def png():
    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data) & 0xffffffff)
    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", 512, 512, 8, 6, 0, 0, 0)) + chunk(b"IDAT", zlib.compress((b"\0" + bytes(512 * 4)) * 512)) + chunk(b"IEND", b"")


def desktop_string(value):
    # Desktop Entry string escapes apply before Exec argument quoting.
    # https://specifications.freedesktop.org/desktop-entry/latest/value-types.html
    escapes = {"s": " ", "n": "\n", "t": "\t", "r": "\r", "\\": "\\"}
    result, index = [], 0
    while index < len(value):
        char = value[index]
        if char == "\\":
            index += 1
            if index >= len(value) or value[index] not in escapes:
                raise AssertionError("invalid Desktop Entry string escape: " + value)
            char = escapes[value[index]]
        result.append(char)
        index += 1
    return "".join(result)


def desktop_executable(value):
    # The installer emits one quoted executable, with no file field arguments.
    # https://specifications.freedesktop.org/desktop-entry/latest/exec-variables.html
    value = desktop_string(value)
    if not value.startswith('"') or not value.endswith('"'):
        raise AssertionError("Exec must quote the whole installed path")
    value = value[1:-1]
    result, index = [], 0
    while index < len(value):
        char = value[index]
        if char == "\\":
            index += 1
            if index >= len(value) or value[index] not in '\\"$`':
                raise AssertionError("invalid quoted Exec escape")
            char = value[index]
        elif char in '"$`':
            raise AssertionError("unescaped reserved Exec character")
        result.append(char)
        index += 1
    value = "".join(result)
    result, index = [], 0
    while index < len(value):
        char = value[index]
        if char == "%":
            index += 1
            if index >= len(value) or value[index] != "%":
                raise AssertionError("unexpected Exec field code")
        result.append(char)
        index += 1
    return "".join(result)


class InstallerTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="oie-installer-test-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.user_dir = self.root / "user"
        self.user_dir.mkdir()
        self.work = self.root / "work"
        self.work.mkdir()
        self.temporary_files = self.root / "temporary files"
        self.temporary_files.mkdir()
        self.commands = self.root / "commands"
        self.commands.mkdir()
        self.requests = self.root / "requests.jsonl"
        self.download_map = self.root / "downloads.json"
        self.map = {}
        self.download_map.write_text("{}")
        self.env = dict(os.environ, HOME=str(self.user_dir), TMPDIR=str(self.temporary_files),
                        PATH=str(self.commands), OIE_TEST_OS="Linux", OIE_TEST_ARCH="x86_64",
                        OIE_TEST_DOWNLOADS=str(self.download_map), OIE_TEST_REQUESTS=str(self.requests),
                        OIE_TEST_PRIVILEGE_LOG=str(self.root / "privilege.log"),
                        OIE_TEST_RUNTIME="available")
        # Limit PATH to known tools. In particular FFmpeg and package managers
        # are absent, and the fake curl never opens a network connection.
        for name in ["awk", "basename", "cat", "chmod", "cmp", "cp", "cut", "date", "dirname", "du",
                     "file", "find", "grep", "gzip", "head", "ln", "ls", "mkdir", "mktemp", "mv", "readlink",
                     "rm", "rmdir", "sed", "sha256sum", "shasum", "sort", "tail", "tar", "tr", "uname", "wc", "xargs"]:
            original = shutil.which(name)
            if original:
                (self.commands / name).symlink_to(original)
        self.command("uname", """import os,sys
print(os.environ['OIE_TEST_OS'] if '-s' in sys.argv else os.environ['OIE_TEST_ARCH'] if '-m' in sys.argv else os.environ['OIE_TEST_OS'])
""")
        self.command("id", "print('1000')\n")
        self.command("sw_vers", "print('26.6.2')\n")
        self.command("ldd", """import os,sys
mode=os.environ['OIE_TEST_RUNTIME']
if mode=='error': print('ldd failed');sys.exit(1)
if mode=='missing': print('libwebkitgtk-6.0.so.4 => not found');sys.exit(0)
print('libgtk-4.so.1 => /fixture/libgtk-4.so.1 (0x1)')
print('libwebkitgtk-6.0.so.4 => /fixture/libwebkitgtk-6.0.so.4 (0x2)')
print('libsoup-3.0.so.0 => /fixture/libsoup-3.0.so.0 (0x3)')
""")
        self.command("ldconfig", """import os
if os.environ['OIE_TEST_RUNTIME']=='available':
 print('libgtk-4.so.1 (libc6,x86-64) => /fixture/libgtk-4.so.1')
 print('libwebkitgtk-6.0.so.4 (libc6,x86-64) => /fixture/libwebkitgtk-6.0.so.4')
 print('libsoup-3.0.so.0 (libc6,x86-64) => /fixture/libsoup-3.0.so.0')
""")
        self.command("curl", """import json,os,pathlib,sys
args=sys.argv[1:];urls=[x for x in args if x.startswith('https://')]
with open(os.environ['OIE_TEST_REQUESTS'],'a') as log:log.write(json.dumps(args)+'\\n')
if len(urls)!=1:print('test curl requires exactly one HTTPS URL',file=sys.stderr);sys.exit(98)
source=json.loads(pathlib.Path(os.environ['OIE_TEST_DOWNLOADS']).read_text()).get(urls[0])
if source is None:print('fixture download failed: '+urls[0],file=sys.stderr);sys.exit(22)
output=None
for n,arg in enumerate(args):
 if arg in ['-o','--output']:output=args[n+1]
 if arg.startswith('--output='):output=arg.split('=',1)[1]
data=pathlib.Path(source).read_bytes()
if output:pathlib.Path(output).write_bytes(data)
else:sys.stdout.buffer.write(data)
""")
        for name in ["sudo", "apt", "apt-get", "brew", "port", "dnf", "yum", "pacman"]:
            self.command(name, """import os,sys
with open(os.environ['OIE_TEST_PRIVILEGE_LOG'],'a') as log:log.write(sys.argv[0]+'\\n')
sys.exit(97)
""")
        self.release()

    def command(self, name, body):
        path = self.commands / name
        if path.is_symlink():
            path.unlink()
        path.write_text("#!" + sys.executable + "\n" + body)
        path.chmod(0o755)

    def release(self, version=VERSION, os_name="linux", architecture="amd64", files=None, members=None):
        asset = "oie_%s_%s_%s.tar.gz" % (version, os_name, architecture)
        archive = self.root / asset
        if files is None:
            if os_name == "linux":
                files = {"bin/oie-desktop": EXECUTABLE,
                         "share/applications/org.araihu.oie.desktop": DESKTOP,
                         "share/icons/hicolor/512x512/apps/org.araihu.oie.png": png(),
                         "share/doc/oie/LICENSE": b"Apache License 2.0 fixture\n",
                         "share/doc/oie/NOTICE": b"Oie fixture attribution\n",
                         "share/doc/oie/README.md": b"Installer fixture documentation\n"}
            else:
                files = {"Oie.app/Contents/MacOS/oie-desktop": EXECUTABLE,
                         "Oie.app/Contents/Info.plist": plistlib.dumps({"CFBundleIdentifier": "org.araihu.oie", "CFBundleExecutable": "oie-desktop", "CFBundleName": "Oiê", "CFBundlePackageType": "APPL", "CFBundleShortVersionString": "0.0.1"}),
                         "Oie.app/Contents/Resources/oie.icns": b"icns\x00\x00\x00\x08",
                         "Oie.app/Contents/Resources/LICENSE": b"Apache License 2.0 fixture\n",
                         "Oie.app/Contents/Resources/NOTICE": b"Oie fixture attribution\n"}
        with tarfile.open(archive, "w:gz", format=tarfile.USTAR_FORMAT) as package:
            for name, data in files.items():
                entry = tarfile.TarInfo(name)
                entry.size = len(data)
                entry.mode = 0o755 if name.endswith("/oie-desktop") else 0o644
                package.addfile(entry, io.BytesIO(data))
            for entry, data in members or []:
                package.addfile(entry, io.BytesIO(data) if data else None)
        manifest = self.root / (asset + ".SHA256SUMS")
        manifest.write_text(hashlib.sha256(archive.read_bytes()).hexdigest() + "  " + asset + "\n")
        self.map[RELEASES + version + "/" + asset] = str(archive)
        self.map[RELEASES + version + "/SHA256SUMS"] = str(manifest)
        self.download_map.write_text(json.dumps(self.map))
        return archive, manifest, asset

    def install(self, *args, success=True):
        result = subprocess.run(["/bin/sh", str(INSTALLER), *map(str, args)], cwd=self.work,
                                env=self.env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                text=True, timeout=15)
        if success:
            self.assertEqual(result.returncode, 0, result.stdout)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertFalse((self.root / "privilege.log").exists(), "installer invoked a package manager or sudo")
        self.assertEqual(list(self.temporary_files.iterdir()), [], "installer left temporary files")
        return result

    def requested_urls(self):
        if not self.requests.exists():
            return []
        return [arg for line in self.requests.read_text().splitlines() for arg in json.loads(line) if arg.startswith("https://")]

    def assert_linux(self, prefix, binary=EXECUTABLE):
        self.assertEqual((prefix / "bin/oie-desktop").read_bytes(), binary)
        self.assertTrue((prefix / "bin/oie-desktop").stat().st_mode & stat.S_IXUSR)
        self.assertTrue((prefix / "share/icons/hicolor/512x512/apps/org.araihu.oie.png").is_file())
        self.assertTrue((prefix / "share/doc/oie/LICENSE").is_file())
        desktop = (prefix / "share/applications/org.araihu.oie.desktop").read_text()
        executable = next(line.split("=", 1)[1] for line in desktop.splitlines() if line.startswith("Exec="))
        self.assertEqual(desktop_executable(executable), str(prefix / "bin/oie-desktop"), "desktop Exec does not resolve to the installed binary")
        self.assertIn("Type=Application", desktop)
        icon = next(line.split("=", 1)[1] for line in desktop.splitlines() if line.startswith("Icon="))
        self.assertEqual(desktop_string(icon), str(prefix / "share/icons/hicolor/512x512/apps/org.araihu.oie.png"))

    def snapshot(self, directory):
        return {str(path.relative_to(directory)): (path.read_bytes(), stat.S_IMODE(path.stat().st_mode))
                for path in directory.rglob("*") if path.is_file()}

    def test_help_is_offline(self):
        output = self.install("--help").stdout
        self.assertIn("--version", output)
        self.assertIn("--prefix", output)
        self.assertEqual(self.requested_urls(), [])

    def test_linux_default_and_optional_ffmpeg(self):
        self.assertFalse((self.commands / "ffmpeg").exists())
        self.install()
        self.assert_linux(self.user_dir / ".local")
        self.assertEqual(set(self.requested_urls()), {RELEASES + VERSION + "/SHA256SUMS", RELEASES + VERSION + "/oie_" + VERSION + "_linux_amd64.tar.gz"})

    def test_linux_custom_prefix_spaces_and_explicit_version(self):
        prefix = self.root / "installation with spaces"
        self.release(version="v0.0.1-rc.1")
        self.install("--prefix", prefix, "--version", "v0.0.1-rc.1")
        self.assert_linux(prefix)
        self.assertTrue(all("/v0.0.1-rc.1/" in url for url in self.requested_urls()))

    def test_desktop_entry_quotes_literal_prefix_characters(self):
        prefix = self.root / 'apps "quoted" $literal `name` 100% \\path'
        self.install("--prefix", prefix)
        self.assert_linux(prefix)

    def test_macos_default_install(self):
        self.env.update(OIE_TEST_OS="Darwin", OIE_TEST_ARCH="arm64")
        self.release(os_name="darwin", architecture="arm64")
        self.install()
        bundle = self.user_dir / "Applications/Oie.app"
        self.assertEqual((bundle / "Contents/MacOS/oie-desktop").read_bytes(), EXECUTABLE)
        self.assertEqual(plistlib.loads((bundle / "Contents/Info.plist").read_bytes())["CFBundleIdentifier"], "org.araihu.oie")
        self.assertTrue(all("darwin_arm64" in url or url.endswith("/SHA256SUMS") for url in self.requested_urls()))

    def test_macos_rosetta_selects_arm_release(self):
        self.env.update(OIE_TEST_OS="Darwin", OIE_TEST_ARCH="x86_64")
        self.command("sysctl", "print('1')\n")
        self.release(os_name="darwin", architecture="arm64")
        self.install()
        self.assertTrue((self.user_dir / "Applications/Oie.app/Contents/MacOS/oie-desktop").is_file())
        self.assertTrue(all("darwin_arm64" in url or url.endswith("/SHA256SUMS") for url in self.requested_urls()))

    def test_macos_custom_prefix_with_spaces(self):
        self.env.update(OIE_TEST_OS="Darwin", OIE_TEST_ARCH="arm64")
        self.release(os_name="darwin", architecture="arm64")
        prefix = self.root / "Mac apps with spaces"
        self.install("--prefix", prefix)
        self.assertEqual((prefix / "Oie.app/Contents/MacOS/oie-desktop").read_bytes(), EXECUTABLE)

    def test_unsupported_platforms_do_not_download(self):
        for platform, architecture in [("Linux", "aarch64"), ("Darwin", "x86_64"), ("FreeBSD", "amd64")]:
            with self.subTest(platform=platform, architecture=architecture):
                self.env.update(OIE_TEST_OS=platform, OIE_TEST_ARCH=architecture)
                self.install(success=False)
                self.assertEqual(self.requested_urls(), [])
                self.assertFalse((self.user_dir / ".local").exists())
                self.assertFalse((self.user_dir / "Applications").exists())

    def test_bad_flags_and_versions_do_not_download(self):
        for args in [("--unknown",), ("--version",), ("--prefix",), ("--version", "../../bad"), ("--version", "v0.0.1;echo unsafe")]:
            with self.subTest(args=args):
                self.install(*args, success=False)
                self.assertEqual(self.requested_urls(), [])

    def test_missing_runtime_only_prints_guidance(self):
        self.env["OIE_TEST_RUNTIME"] = "missing"
        output = self.install(success=False).stdout
        self.assertIn("apt", output.lower())
        self.assertIn("libgtk-4", output.lower())
        self.assertIn("libwebkitgtk-6.0", output.lower())
        self.assertFalse((self.user_dir / ".local/bin/oie-desktop").exists())

    def test_runtime_ldconfig_fallback(self):
        (self.commands / "ldd").unlink()
        self.install()
        self.assert_linux(self.user_dir / ".local")

    def test_failed_runtime_check_does_not_install(self):
        self.env["OIE_TEST_RUNTIME"] = "error"
        self.install(success=False)
        self.assertFalse((self.user_dir / ".local/bin/oie-desktop").exists())

    def test_missing_runtime_tools_does_not_install(self):
        (self.commands / "ldd").unlink()
        (self.commands / "ldconfig").unlink()
        self.install(success=False)
        self.assertFalse((self.user_dir / ".local/bin/oie-desktop").exists())

    def test_checksum_mismatch(self):
        archive, _, _ = self.release()
        archive.write_bytes(archive.read_bytes() + b"tampered")
        self.install(success=False)
        self.assertFalse((self.user_dir / ".local/bin/oie-desktop").exists())

    def test_missing_manifest(self):
        self.map.pop(RELEASES + VERSION + "/SHA256SUMS")
        self.download_map.write_text(json.dumps(self.map))
        self.install(success=False)
        self.assertFalse((self.user_dir / ".local/bin/oie-desktop").exists())

    def test_no_matching_checksum(self):
        _, manifest, _ = self.release()
        manifest.write_text("0" * 64 + "  different-asset.tar.gz\n")
        self.install(success=False)
        self.assertFalse((self.user_dir / ".local/bin/oie-desktop").exists())

    def test_duplicate_checksum_is_rejected(self):
        _, manifest, _ = self.release()
        manifest.write_text(manifest.read_text() * 2)
        self.install(success=False)
        self.assertFalse((self.user_dir / ".local/bin/oie-desktop").exists())

    def test_failed_archive_download(self):
        _, _, asset = self.release()
        self.map.pop(RELEASES + VERSION + "/" + asset)
        self.download_map.write_text(json.dumps(self.map))
        self.install(success=False)
        self.assertFalse((self.user_dir / ".local/bin/oie-desktop").exists())

    def test_malformed_archive_with_valid_checksum(self):
        archive, manifest, asset = self.release()
        archive.write_bytes(b"not a tar archive")
        manifest.write_text(hashlib.sha256(archive.read_bytes()).hexdigest() + "  " + asset + "\n")
        self.install(success=False)
        self.assertFalse((self.user_dir / ".local/bin/oie-desktop").exists())

    def test_missing_expected_binary(self):
        self.release(files={"README.md": b"no application"})
        self.install(success=False)
        self.assertFalse((self.user_dir / ".local/bin/oie-desktop").exists())

    def test_archive_traversal_absolute_and_links_are_rejected(self):
        for kind in ["traversal", "absolute", "symlink", "hardlink"]:
            with self.subTest(kind=kind):
                name = "../escape-sentinel" if kind == "traversal" else str(self.root / "escape-sentinel") if kind == "absolute" else "bin/unsafe"
                member = tarfile.TarInfo(name)
                if kind in ["symlink", "hardlink"]:
                    member.type = tarfile.SYMTYPE if kind == "symlink" else tarfile.LNKTYPE
                    member.linkname = str(self.root / "escape-sentinel")
                    data = b""
                else:
                    data = b"must not extract"
                    member.size = len(data)
                self.release(members=[(member, data)])
                self.install(success=False)
                self.assertFalse((self.root / "escape-sentinel").exists())
                self.assertFalse((self.user_dir / ".local/bin/oie-desktop").exists())

    def test_update_preserves_user_data_and_unrelated_files(self):
        self.install()
        prefix = self.user_dir / ".local"
        data = prefix / "share/oie/engine.sqlite"
        data.parent.mkdir(parents=True)
        data.write_bytes(b"user database fixture")
        unrelated = prefix / "keep.txt"
        unrelated.write_bytes(b"unrelated install")
        updated = EXECUTABLE.replace(b"fixture one", b"fixture two")
        self.release(files={"bin/oie-desktop": updated, "share/applications/org.araihu.oie.desktop": DESKTOP,
                            "share/icons/hicolor/512x512/apps/org.araihu.oie.png": png(), "share/doc/oie/LICENSE": b"Updated fixture license"})
        self.install()
        self.assert_linux(prefix, updated)
        self.assertEqual(data.read_bytes(), b"user database fixture")
        self.assertEqual(unrelated.read_bytes(), b"unrelated install")

    def test_failed_update_preserves_previous_installation(self):
        self.install()
        prefix = self.user_dir / ".local"
        before = self.snapshot(prefix)
        archive, _, _ = self.release()
        archive.write_bytes(archive.read_bytes() + b"changed after checksum")
        self.install(success=False)
        self.assertEqual(self.snapshot(prefix), before)

    def test_macos_update_and_failed_update_preserve_app_data(self):
        self.env.update(OIE_TEST_OS="Darwin", OIE_TEST_ARCH="arm64")
        self.release(os_name="darwin", architecture="arm64")
        self.install()
        prefix = self.user_dir / "Applications"
        data = self.user_dir / "Library/Application Support/Oiê/engine.sqlite"
        data.parent.mkdir(parents=True)
        data.write_bytes(b"user Mac database fixture")
        unrelated = prefix / "keep.txt"
        unrelated.write_bytes(b"another app fixture")
        updated = EXECUTABLE.replace(b"fixture one", b"fixture two")
        self.release(os_name="darwin", architecture="arm64", files={
            "Oie.app/Contents/MacOS/oie-desktop": updated,
            "Oie.app/Contents/Info.plist": plistlib.dumps({"CFBundleIdentifier": "org.araihu.oie", "CFBundleExecutable": "oie-desktop"}),
        })
        self.install()
        self.assertEqual((prefix / "Oie.app/Contents/MacOS/oie-desktop").read_bytes(), updated)
        before = self.snapshot(prefix)
        archive, _, _ = self.release(os_name="darwin", architecture="arm64")
        archive.write_bytes(archive.read_bytes() + b"checksum mismatch")
        self.install(success=False)
        self.assertEqual(self.snapshot(prefix), before)
        self.assertEqual(data.read_bytes(), b"user Mac database fixture")
        self.assertEqual(unrelated.read_bytes(), b"another app fixture")

    def test_failed_backup_move_preserves_original(self):
        self.install()
        prefix = self.user_dir / ".local"
        before = self.snapshot(prefix)
        original_mv = shutil.which("mv")
        marker = self.root / "backup-move-failed"
        self.env.update(OIE_TEST_BACKUP_SOURCE=str(prefix / "bin/oie-desktop"), OIE_TEST_BACKUP_MARKER=str(marker))
        self.command("mv", """import os,pathlib,sys
marker=pathlib.Path(os.environ['OIE_TEST_BACKUP_MARKER'])
if len(sys.argv)>=3 and sys.argv[-2]==os.environ['OIE_TEST_BACKUP_SOURCE'] and sys.argv[-1].endswith('/old-bin') and not marker.exists():
 marker.write_text('backup refused');sys.exit(73)
os.execv(%r,[%r]+sys.argv[1:])
""" % (original_mv, original_mv))
        self.install(success=False)
        self.assertTrue(marker.exists(), "test did not exercise an actual backup move failure")
        self.assertEqual(self.snapshot(prefix), before)

    def test_sigterm_after_backup_move_restores_original(self):
        self.install()
        prefix = self.user_dir / ".local"
        before = self.snapshot(prefix)
        original_mv = shutil.which("mv")
        marker = self.root / "backup-signal-sent"
        self.env.update(OIE_TEST_BACKUP_SOURCE=str(prefix / "bin/oie-desktop"), OIE_TEST_BACKUP_MARKER=str(marker))
        self.command("mv", """import os,pathlib,signal,subprocess,sys
marker=pathlib.Path(os.environ['OIE_TEST_BACKUP_MARKER'])
if len(sys.argv)>=3 and sys.argv[-2]==os.environ['OIE_TEST_BACKUP_SOURCE'] and sys.argv[-1].endswith('/old-bin') and not marker.exists():
 subprocess.run([%r]+sys.argv[1:],check=True)
 marker.write_text('backup completed; signal sent')
 os.kill(os.getppid(),signal.SIGTERM)
 sys.exit(0)
os.execv(%r,[%r]+sys.argv[1:])
""" % (original_mv, original_mv, original_mv))
        self.install(success=False)
        self.assertTrue(marker.exists(), "test did not signal after an actual backup rename")
        self.assertEqual(self.snapshot(prefix), before)

    def test_mid_install_failure_rolls_back_previous_files(self):
        self.install()
        prefix = self.user_dir / ".local"
        before = self.snapshot(prefix)
        original_mv = shutil.which("mv")
        fail_marker = self.root / "move-failed"
        destination = prefix / "share/icons/hicolor/512x512/apps/org.araihu.oie.png"
        self.env.update(OIE_TEST_FAIL_DEST=str(destination), OIE_TEST_FAIL_MARKER=str(fail_marker))
        self.command("mv", """import os,pathlib,sys
marker=pathlib.Path(os.environ['OIE_TEST_FAIL_MARKER'])
if os.environ['OIE_TEST_FAIL_DEST'] in sys.argv[1:] and not marker.exists():
 marker.write_text('failed once');sys.exit(73)
os.execv(%r,[%r]+sys.argv[1:])
""" % (original_mv, original_mv))
        self.release(files={"bin/oie-desktop": EXECUTABLE.replace(b"fixture one", b"fixture two"),
                            "share/applications/org.araihu.oie.desktop": DESKTOP,
                            "share/icons/hicolor/512x512/apps/org.araihu.oie.png": png(), "share/doc/oie/LICENSE": b"Updated license"})
        self.install(success=False)
        self.assertTrue(fail_marker.exists(), "test did not exercise an actual install move failure")
        self.assertEqual(self.snapshot(prefix), before)


if __name__ == "__main__":
    unittest.main()
