#!/bin/sh
# Install gtide (and the optional tide alias) on macOS or Linux.
#
#   curl -fsSL https://github.com/Cellestias/git-tide/releases/latest/download/install.sh | sh
#
# Environment:
#   GTIDE_VERSION    release to install, e.g. v1.1.0 (default: the latest)
#   GTIDE_BIN_DIR    where to put the binaries (default: /usr/local/bin)
#   GTIDE_NO_ALIAS   set to 1 to skip the `tide` alias
#   GTIDE_REPO_URL   base URL of the repository (default: the GitHub repo; for mirrors and tests)
set -eu

repo=${GTIDE_REPO_URL:-https://github.com/Cellestias/git-tide}
bindir=${GTIDE_BIN_DIR:-/usr/local/bin}

say() { printf '%s\n' "$*"; }
die() { printf 'install: %s\n' "$*" >&2; exit 1; }

command -v curl >/dev/null 2>&1 || die "curl is required"
command -v tar >/dev/null 2>&1 || die "tar is required"

case $(uname -s) in
Darwin) os=darwin ;;
Linux) os=linux ;;
*) die "unsupported system: $(uname -s). On Windows use install.ps1" ;;
esac
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) die "unsupported CPU: $(uname -m)" ;;
esac

version=${GTIDE_VERSION:-}
if [ -z "$version" ]; then
	version=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$repo/releases/latest" | sed 's#.*/##')
fi
case "$version" in
v[0-9]*) ;;
*) die "could not work out the version (got '$version'); set GTIDE_VERSION=vX.Y.Z" ;;
esac

name=gtide_${version}_${os}_${arch}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "Downloading $name"
curl -fsSL -o "$tmp/$name.tar.gz" "$repo/releases/download/$version/$name.tar.gz" || die "download failed: $repo/releases/download/$version/$name.tar.gz"
curl -fsSL -o "$tmp/checksums.txt" "$repo/releases/download/$version/checksums.txt" || die "could not download checksums.txt"

expected=$(grep " $name.tar.gz\$" "$tmp/checksums.txt" | cut -d' ' -f1)
[ -n "$expected" ] || die "$name.tar.gz is not listed in checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
	actual=$(sha256sum "$tmp/$name.tar.gz" | cut -d' ' -f1)
else
	actual=$(shasum -a 256 "$tmp/$name.tar.gz" | cut -d' ' -f1)
fi
[ "$expected" = "$actual" ] || die "checksum mismatch for $name.tar.gz; nothing was installed"
say "Checksum OK"

tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
src=$tmp/$name

# Use sudo only when the target directory cannot be written otherwise.
SUDO=
if ! { mkdir -p "$bindir" 2>/dev/null && [ -w "$bindir" ]; }; then
	command -v sudo >/dev/null 2>&1 || die "cannot write to $bindir; set GTIDE_BIN_DIR to a directory you own"
	say "$bindir needs administrator rights; sudo may ask for your password"
	SUDO=sudo
	$SUDO mkdir -p "$bindir"
fi

$SUDO install -m 755 "$src/gtide" "$bindir/gtide"
[ "$os" = darwin ] && xattr -d com.apple.quarantine "$bindir/gtide" 2>/dev/null || true
say "Installed $bindir/gtide"

if [ "${GTIDE_NO_ALIAS:-}" != 1 ]; then
	# Never overwrite a different program called tide.
	if [ -e "$bindir/tide" ] && ! "$bindir/tide" --version 2>/dev/null | grep -q '^gtide version'; then
		say "Skipped the tide alias: $bindir/tide is a different program. Use gtide."
	else
		$SUDO install -m 755 "$src/tide" "$bindir/tide"
		[ "$os" = darwin ] && xattr -d com.apple.quarantine "$bindir/tide" 2>/dev/null || true
		say "Installed $bindir/tide (alias)"
	fi
fi

case ":$PATH:" in
*":$bindir:"*) ;;
*) say "Note: $bindir is not on your PATH. Add it, or Git hooks will skip their checks." ;;
esac

"$bindir/gtide" --version
