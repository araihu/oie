#!/bin/bash
# Index the verified release packages and sign their repositories for GitHub Pages.
set -euo pipefail
root=$(realpath "$1")
packages=$(realpath "$2")
keyring=$(realpath "$3")
fingerprint=$4
base_url=https://araihu.github.io/oie
scratch=$(mktemp -d)
trap 'rm -r "$scratch"' EXIT

shopt -s nullglob
rpms=("$packages"/*.rpm)
arch_packages=("$packages"/*.pkg.tar.zst)
bundles=("$packages"/*.flatpak)
[[ ${#rpms[@]} == 1 && ${#arch_packages[@]} == 1 && ${#bundles[@]} == 1 ]]

rpm_repo=$root/rpm/x86_64
arch_repo=$root/pacman/x86_64
flatpak_repo=$root/flatpak
mkdir -p "$rpm_repo" "$arch_repo" "$flatpak_repo" "$scratch/rpmdb"
gpg=(gpg --batch --homedir "$keyring")
gpgv=(gpgv --keyring "$scratch/oie.gpg")
"${gpg[@]}" --export-options export-minimal --export "$fingerprint" > "$scratch/oie.gpg"
"${gpg[@]}" --armor --export-options export-minimal --export "$fingerprint" > "$root/oie-archive-keyring.asc"

rpm_package=$scratch/${rpms[0]##*/}
cp "${rpms[0]}" "$rpm_package"
[[ $(rpm --dbpath "$scratch/rpmdb" -qp --queryformat '%{NAME} %{ARCH}' "$rpm_package") == 'oie x86_64' ]]
GNUPGHOME="$keyring" rpmsign --dbpath "$scratch/rpmdb" --define "__gpg $(command -v gpg)" \
  --define "_gpg_name $fingerprint" --addsign "$rpm_package"
rpmkeys --dbpath "$scratch/rpmdb" --import "$root/oie-archive-keyring.asc"
rpmkeys --dbpath "$scratch/rpmdb" --checksig "$rpm_package"
# RPM signing changes the bytes. Give each signed payload an immutable URL.
hash=$(sha256sum "$rpm_package" | cut -d ' ' -f 1)
mkdir -p "$rpm_repo/Packages/$hash"
mv "$rpm_package" "$rpm_repo/Packages/$hash/"
createrepo_c --checksum sha256 "$rpm_repo"
repomd=$rpm_repo/repodata/repomd.xml
"${gpg[@]}" --local-user "$fingerprint" --armor --detach-sign "$repomd"
"${gpgv[@]}" "$repomd.asc" "$repomd"
cat > "$root/rpm/oie.repo" <<CONFIG
[araihu-oie]
name=Arai Hû Oiê
baseurl=$base_url/rpm/\$basearch/
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=$base_url/oie-archive-keyring.asc
includepkgs=oie
CONFIG

arch_package=${arch_packages[0]##*/}
cp "${arch_packages[0]}" "$arch_repo/"
(
  cd "$arch_repo"
  bsdtar -xOf "$arch_package" .PKGINFO > "$scratch/PKGINFO"
  grep -Fxq 'pkgname = oie' "$scratch/PKGINFO"
  grep -Fxq 'arch = x86_64' "$scratch/PKGINFO"
  "${gpg[@]}" --local-user "$fingerprint" --detach-sign "$arch_package"
  "${gpgv[@]}" "$arch_package.sig" "$arch_package"
  repo-add araihu-oie.db.tar.gz "$arch_package"
  # GitHub Pages artifacts cannot contain the symlinks created by repo-add.
  for index in araihu-oie.db araihu-oie.files; do
    unlink "$index"
    mv "$index.tar.gz" "$index"
    "${gpg[@]}" --local-user "$fingerprint" --detach-sign "$index"
    "${gpgv[@]}" "$index.sig" "$index"
  done
)

ref=app/com.araihu.oie/x86_64/master
ostree --repo="$flatpak_repo" init --mode=archive
flatpak build-import-bundle --gpg-homedir="$keyring" --gpg-sign="$fingerprint" \
  --no-update-summary "$flatpak_repo" "${bundles[0]}"
commit=$(ostree --repo="$flatpak_repo" rev-parse "$ref")
flatpak build-update-repo --gpg-homedir="$keyring" --gpg-sign="$fingerprint" \
  --generate-static-deltas --static-delta-jobs=2 "$flatpak_repo"
cat > "$flatpak_repo/oie.flatpakrepo" <<CONFIG
[Flatpak Repo]
Title=Arai Hû Oiê
Url=$base_url/flatpak/
Homepage=https://oie.araihu.com/
GPGKey=$(base64 -w0 < "$scratch/oie.gpg")
CONFIG
export FLATPAK_USER_DIR=$scratch/flatpak
flatpak remote-add --user --gpg-import="$scratch/oie.gpg" araihu-oie "file://$flatpak_repo"
[[ $(flatpak remote-info --user --show-commit araihu-oie "$ref") == "$commit" ]]

cd "$root"
sha256sum oie-archive-keyring.asc rpm/oie.repo rpm/x86_64/repodata/repomd.xml{,.asc} \
  pacman/x86_64/araihu-oie.db{,.sig} flatpak/oie.flatpakrepo flatpak/summary{,.sig} \
  > linux-repositories.sha256
