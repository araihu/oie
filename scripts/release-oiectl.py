#!/usr/bin/env python3
"""Build and publish oiectl from a tagged engine contract."""

import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
from tempfile import TemporaryDirectory

SPEC_PREFIX = "openapi/engine/"
CLI_PREFIX = "cli/"
SPEC = "api/openapi.yaml"
METADATA = Path("cli/cmd/oiectl/release.json")
TARGETS = (("linux", "amd64"), ("darwin", "arm64"))
NUMBER = r"(?:0|[1-9][0-9]*)"
IDENTIFIER = rf"(?:{NUMBER}|[0-9]*[A-Za-z-][0-9A-Za-z-]*)"
VERSION = re.compile(rf"v({NUMBER})\.({NUMBER})\.({NUMBER})(-{IDENTIFIER}(?:\.{IDENTIFIER})*)?")


def run(*command, capture=False, **kwargs):
    if capture:
        kwargs["stdout"] = subprocess.PIPE
    result = subprocess.run(command, text=True, check=True, **kwargs)
    return result.stdout.strip() if capture else None


def git(*args):
    return run("git", *args, capture=True)


def parse_tag(tag, prefix):
    version = tag.removeprefix(prefix)
    match = VERSION.fullmatch(version) if version != tag else None
    if match is None:
        raise ValueError(f"expected a SemVer tag with prefix {prefix}: {tag}")
    return match


def plan(spec_tag, spec_revision, request_id, source_revision):
    major, minor, _, prerelease = parse_tag(spec_tag, SPEC_PREFIX).groups()
    prefix = f"{CLI_PREFIX}v{major}.{minor}."
    latest_patch = -1
    identity = {"specTag": spec_tag, "specRevision": spec_revision, "sourceRevision": source_revision, "request": request_id}
    for tag in git("tag", "--list", f"{prefix}*").splitlines():
        version = VERSION.fullmatch(tag.removeprefix(CLI_PREFIX))
        if version is None:
            continue
        latest_patch = max(latest_patch, int(version[3]))
        if not git("ls-tree", tag, "--", str(METADATA)):
            continue
        metadata = json.loads(git("show", f"{tag}:{METADATA}"))
        if metadata.get("specTag") == spec_tag and metadata.get("specRevision") != spec_revision:
            raise ValueError(f"{spec_tag} no longer points to the spec used for {tag}")
        if all(metadata.get(key) == value for key, value in identity.items()):
            if metadata.get("version") != tag.removeprefix(CLI_PREFIX):
                raise ValueError(f"inconsistent release metadata at {tag}")
            return tag, metadata, True
    version = f"v{major}.{minor}.{latest_patch + 1}{prerelease or ''}"
    return CLI_PREFIX + version, {"version": version, **identity}, False


def module_path(major):
    return "github.com/araihu/oie/cli" + (f"/v{major}" if int(major) >= 2 else "")


def build_assets(output, metadata):
    version, spec_tag = metadata["version"], metadata["specTag"]
    expected = f"oiectl version {version} (spec {spec_tag})"
    env = {**os.environ, "CGO_ENABLED": "0", "GOFLAGS": "", "GOWORK": "off"}
    with TemporaryDirectory() as temporary:
        staging = Path(temporary)
        run("go", "-C", "cli", "test", "-mod=readonly", "./...", env=env)
        build = ("go", "-C", "cli", "build", "-mod=readonly", "-trimpath", "-buildvcs=false")
        host = tuple(run("go", "env", "GOHOSTOS", "GOHOSTARCH", capture=True, env=env).splitlines())
        for system, arch in dict.fromkeys((*TARGETS, host)):
            target = {**env, "GOOS": system, "GOARCH": arch}
            name = f"oiectl_{version}_{system}_{arch}"
            payload = staging / name
            payload.mkdir()
            binary = payload / "oiectl"
            run(*build, "-o", str(binary), "./cmd/oiectl", env=target)
            if (system, arch) == host:
                actual = run(str(binary), "--version", capture=True)
                if actual != expected:
                    raise ValueError(f"expected {expected!r}, got {actual!r}")
            if (system, arch) not in TARGETS:
                continue
            copy_licenses(payload, target)
            with tarfile.open(output / f"{name}.tar.gz", "w:gz") as archive:
                archive.add(payload, arcname=name)
        source = output / f"oiectl_{version}_source.tar.gz"
        package_source(source, version, env)
    assets = sorted(output.glob("*.tar.gz"))
    sums = []
    for asset in assets:
        with asset.open("rb") as stream:
            sums.append(f"{hashlib.file_digest(stream, 'sha256').hexdigest()}  {asset.name}\n")
    (output / "SHA256SUMS").write_text("".join(sums))


def package_source(output, version, env):
    prefix = f"oiectl_{version}_source"
    tree = subprocess.check_output(["git", "archive", "HEAD"])
    with tarfile.open(output, "x:gz") as archive, tarfile.open(fileobj=io.BytesIO(tree)) as source:
        for member in source:
            data = source.extractfile(member) if member.isfile() else None
            member.name = f"{prefix}/oie/{member.name}"
            archive.addfile(member, data)
        template = "{{if not .Main}}{{.Path}}@{{.Version}}{{end}}"
        packages = "{{with .Module}}" + template + "{{end}}"
        modules = set()
        for system, arch in TARGETS:
            target = {**env, "GOOS": system, "GOARCH": arch}
            modules.update(run("go", "-C", "cli", "list", "-mod=readonly", "-deps", "-f", packages,
                               "./cmd/oiectl", capture=True, env=target).split())
        # The generator's runtime and templates are copied into the CLI tree.
        generator = "github.com/araihu/oapi-cli-gen"
        pin = run("go", "-C", "tools", "list", "-mod=readonly", "-m", "-f", "{{.Version}}", generator, capture=True, env=env)
        modules.add(f"{generator}@{pin}")
        for module in sorted(modules):
            data = json.loads(run("go", "-C", "cli", "mod", "download", "-json", module, capture=True, env=env))
            path = re.sub(r"[A-Z]", lambda match: "!" + match[0].lower(), data["Path"])
            for key in ("Info", "GoMod", "Zip"):
                file = Path(data[key])
                archive.add(file, arcname=f"{prefix}/go-proxy/{path}/@v/{file.name}")
        # Go can read module manifests outside the packages linked into oiectl.
        cache = Path(run("go", "env", "GOMODCACHE", capture=True, env=env)) / "cache/download"
        manifests = run("go", "-C", "cli", "list", "-mod=readonly", "-m", "-f", template, "all", capture=True, env=env)
        for module in sorted(set(manifests.split()) - modules):
            path, version = module.split("@", 1)
            path = re.sub(r"[A-Z]", lambda match: "!" + match[0].lower(), path)
            file = cache / path / "@v" / f"{version}.mod"
            if file.is_file():
                archive.add(file, arcname=f"{prefix}/go-proxy/{path}/@v/{file.name}")


def copy_licenses(payload, env):
    for file in ("LICENSE", "THIRD_PARTY_NOTICES.md"):
        shutil.copy2(file, payload)
    licenses = payload / "licenses"
    licenses.mkdir()
    (licenses / "oapi-cli-gen").mkdir()
    shutil.copy2("cli/internal/cliruntime/LICENSE", licenses / "oapi-cli-gen" / "LICENSE")
    go_root = Path(run("go", "env", "GOROOT", capture=True, env=env))
    shutil.copy2(go_root / "LICENSE", licenses / "go.LICENSE")
    template = '{{with .Module}}{{if not .Main}}{{.Path}}{{"\t"}}{{.Dir}}{{end}}{{end}}'
    modules = run("go", "-C", "cli", "list", "-mod=readonly", "-deps", "-f", template, "./cmd/oiectl", capture=True, env=env)
    for line in sorted(set(modules.splitlines()) - {""}):
        name, directory = line.split("\t")
        for file in Path(directory).iterdir():
            if file.is_file() and file.name.startswith(("LICENSE", "LICENCE", "COPYING", "NOTICE", "PATENTS")):
                destination = licenses / name
                destination.mkdir(parents=True, exist_ok=True)
                shutil.copy2(file, destination)


def prepare_source(spec_tag, spec_revision, metadata):
    git("switch", "--detach", metadata["sourceRevision"])
    run("git", "diff", "--exit-code", spec_revision, "HEAD", "--", SPEC)
    major = parse_tag(spec_tag, SPEC_PREFIX)[1]
    module = module_path(major)
    current = run("go", "-C", "cli", "list", "-m", "-f", "{{.Path}}", capture=True)
    if current != module:
        run("go", "-C", "cli", "mod", "edit", "-module", module)
        # Keep the old checksums so generation still verifies the managed files.
        manifest = Path("cli/.oapi-cli-gen.json")
        previous = json.loads(manifest.read_text())
        previous["module"] = module
        manifest.write_text(json.dumps(previous))
        entrypoint = METADATA.with_name("oiectl.go")
        entrypoint.write_text(entrypoint.read_text().replace(f'"{current}/internal/', f'"{module}/internal/'))
    version = spec_tag.removeprefix(f"{SPEC_PREFIX}v")
    run("make", "generate-cli", f"OIECTL_SPEC_VERSION={version}", stdout=subprocess.DEVNULL)
    METADATA.write_text(json.dumps(metadata, indent=2) + "\n")
    git("add", "--", "api/openapi.cli.json", "cli")
    git("-c", "user.name=github-actions[bot]", "-c", "user.email=41898282+github-actions[bot]@users.noreply.github.com",
        "commit", "-m", f"Release oiectl {metadata['version']} for {spec_tag}")


def write_notes(tag, metadata, output, repository):
    notes = output / "release-notes.md"
    spec_tag = metadata["specTag"]
    version = metadata["version"]
    module = module_path(parse_tag(tag, CLI_PREFIX)[1])
    notes.write_text(
        f"Generated from the engine contract tagged [`{spec_tag}`]"
        f"(https://github.com/{repository}/tree/{spec_tag}/api).\n\n"
        "Download the archive for Linux x86-64 or macOS Apple Silicon and verify it against `SHA256SUMS`. "
        "The source archive contains the tagged tree and Go dependency sources.\n\n"
        f"```sh\ngo install {module}/cmd/oiectl@{version}\noiectl --version\n```\n"
    )


def publish(tag, metadata, output, repository, draft):
    notes = output / "release-notes.md"
    version = metadata["version"]
    assets = sorted(str(path) for path in output.glob("*.tar.gz")) + [str(output / "SHA256SUMS")]
    if draft:
        run("gh", "release", "upload", tag, "--repo", repository, "--clobber", *assets)
        run("gh", "release", "edit", tag, "--repo", repository, "--draft=false", "--latest=false", "--notes-file", str(notes))
    else:
        prerelease = ["--prerelease"] if parse_tag(tag, CLI_PREFIX)[4] else []
        run("gh", "release", "create", tag, "--repo", repository, "--verify-tag", "--latest=false",
            "--title", f"oiectl {version}", "--notes-file", str(notes), *prerelease, *assets)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("spec_tag")
    parser.add_argument("--patch", metavar="REQUEST_ID", help="Release a CLI-only patch from the current revision")
    parser.add_argument("--publish", metavar="OWNER/REPO", help="Push the CLI tag and publish on GitHub")
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    # Release commits belong in a disposable checkout.
    if git("status", "--porcelain"):
        raise ValueError("start from a clean release checkout")
    parse_tag(args.spec_tag, SPEC_PREFIX)
    spec_revision = git("rev-parse", "--verify", f"refs/tags/{args.spec_tag}^{{commit}}")
    head = git("rev-parse", "HEAD")
    if os.environ.get("GITHUB_EVENT_NAME") == "push" and spec_revision != head:
        raise ValueError(f"{args.spec_tag} does not point to the workflow's source commit")
    source = head if args.patch else spec_revision
    request_id = f"patch:{args.patch}" if args.patch else "spec"
    tag, metadata, exists = plan(args.spec_tag, spec_revision, request_id, source)
    release = ""
    if args.publish and exists:
        release = run("gh", "api", "--paginate", f"repos/{args.publish}/releases?per_page=100",
                      "--jq", f'.[] | select(.tag_name == "{tag}") | .draft', capture=True)
        if release == "false":
            print(f"{tag} is already published")
            return
        if release not in ("", "true"):
            raise ValueError(f"unexpected release state for {tag}: {release}")
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    if any(output.iterdir()):
        raise ValueError("release output directory must be empty")
    if exists:
        git("switch", "--detach", tag)
    else:
        prepare_source(args.spec_tag, spec_revision, metadata)
    build_assets(output, metadata)
    write_notes(tag, metadata, output, args.publish or "araihu/oie")
    if args.publish:
        if not exists:
            git("tag", tag)
            run("git", "push", "origin", f"refs/tags/{tag}")
        publish(tag, metadata, output, args.publish, release == "true")
    print(tag)


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        sys.exit(str(error))
