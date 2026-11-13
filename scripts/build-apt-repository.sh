#!/bin/bash
# Generate an APT archive from release .deb files in apt/pool/main/o/oie.
set -euo pipefail
root=$(realpath "$1")
keyring=$(realpath "$2")
fingerprint=$3
cd "$root/apt"
index=dists/oie/main/binary-amd64
mkdir -p "$index"
shopt -s nullglob
packages=(pool/main/o/oie/*.deb)
(( ${#packages[@]} > 0 ))
for package in "${packages[@]}"; do
  [[ $(dpkg-deb -f "$package" Package) == oie ]]
  [[ $(dpkg-deb -f "$package" Architecture) == amd64 ]]
done
dpkg-scanpackages --multiversion pool /dev/null > "$index/Packages"
gzip -n -9 -c "$index/Packages" > "$index/Packages.gz"
for file in "$index/Packages" "$index/Packages.gz"; do
  hash=$(sha256sum "$file" | cut -d ' ' -f 1)
  mkdir -p "$index/by-hash/SHA256"
  cp "$file" "$index/by-hash/SHA256/$hash"
done
apt-ftparchive \
  -o APT::FTPArchive::Release::Origin='Arai Hû' \
  -o APT::FTPArchive::Release::Label='Arai Hû Oie' \
  -o APT::FTPArchive::Release::Suite=oie \
  -o APT::FTPArchive::Release::Codename=oie \
  -o APT::FTPArchive::Release::Architectures=amd64 \
  -o APT::FTPArchive::Release::Components=main \
  -o APT::FTPArchive::Release::Acquire-By-Hash=yes \
  release dists/oie > "$root/Release"
mv "$root/Release" dists/oie/Release

# The CI keyring contains only the dedicated signing subkey and public primary key.
gpg --batch --homedir "$keyring" --local-user "$fingerprint" --digest-algo SHA512 \
  --clearsign --output dists/oie/InRelease dists/oie/Release
gpg --batch --homedir "$keyring" --export-options export-minimal \
  --export "$fingerprint" > "$root/apt/oie-archive-keyring.gpg"
gpgv --keyring "$root/apt/oie-archive-keyring.gpg" dists/oie/InRelease
{
  cat <<'SOURCES'
Types: deb
URIs: https://araihu.github.io/oie/apt/
Suites: oie
Components: main
Architectures: amd64
Signed-By:
SOURCES
  gpg --batch --homedir "$keyring" --armor --export-options export-minimal \
    --export "$fingerprint" | sed -e 's/^$/./' -e 's/^/ /'
} > "$root/apt/oie.sources"
