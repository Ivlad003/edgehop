#!/bin/sh
# edgehop installer for macOS and Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/Ivlad003/edgehop/main/install.sh | sh
#
# Downloads the latest release for this computer, checks its SHA-256 checksum
# and puts it into ~/.local/bin (no sudo needed).
#
# Optional environment variables:
#   EDGEHOP_INSTALL_DIR   where to put the program (default: ~/.local/bin)
#   EDGEHOP_VERSION       a specific version such as v0.1.0 (default: latest)
#   EDGEHOP_REPO          owner/name of the GitHub repository
set -eu

repo="${EDGEHOP_REPO:-Ivlad003/edgehop}"
dir="${EDGEHOP_INSTALL_DIR:-$HOME/.local/bin}"
version="${EDGEHOP_VERSION:-latest}"

fail() {
	echo "edgehop install: $*" >&2
	exit 1
}

case "$(uname -s)" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) fail "this script supports macOS and Linux; on Windows use install.ps1 (see https://github.com/$repo)" ;;
esac

case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) fail "unsupported processor: $(uname -m)" ;;
esac

file="edgehop-$os-$arch"
if [ "$version" = latest ]; then
	base="https://github.com/$repo/releases/latest/download"
else
	base="https://github.com/$repo/releases/download/$version"
fi

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -q -O "$2" "$1"; }
else
	fail "curl or wget is required"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $file ($version) ..."
fetch "$base/$file" "$tmp/$file" || fail "could not download $base/$file"
fetch "$base/SHA256SUMS.txt" "$tmp/SHA256SUMS.txt" || fail "could not download the checksum list"

want=$(awk -v f="$file" '$2 == f { print $1 }' "$tmp/SHA256SUMS.txt")
[ -n "$want" ] || fail "no checksum for $file in SHA256SUMS.txt"
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$tmp/$file" | awk '{ print $1 }')
elif command -v shasum >/dev/null 2>&1; then
	got=$(shasum -a 256 "$tmp/$file" | awk '{ print $1 }')
else
	fail "sha256sum or shasum is required to verify the download"
fi
[ "$got" = "$want" ] || fail "checksum mismatch for $file - the download is damaged or was tampered with"

mkdir -p "$dir"
chmod +x "$tmp/$file"
mv -f "$tmp/$file" "$dir/edgehop"
if [ "$os" = darwin ] && command -v xattr >/dev/null 2>&1; then
	xattr -d com.apple.quarantine "$dir/edgehop" 2>/dev/null || true
fi

echo "Installed $("$dir/edgehop" version) to $dir/edgehop"
echo

case ":$PATH:" in
*":$dir:"*) run=edgehop ;;
*)
	run="$dir/edgehop"
	echo "$dir is not in your PATH. To be able to type just 'edgehop', add this"
	echo "line to your shell profile (~/.zshrc, ~/.bashrc ...):"
	echo
	echo "  export PATH=\"$dir:\$PATH\""
	echo
	;;
esac

if [ "$os" = darwin ]; then
	echo "macOS: allow your terminal under Accessibility and Input Monitoring in"
	echo "System Settings > Privacy & Security (edgehop asks on first start)."
else
	echo "Linux: edgehop needs access to /dev/uinput and /dev/input. Give it once with:"
	echo
	echo "  sudo usermod -aG input \$USER"
	echo "  echo 'KERNEL==\"uinput\", GROUP=\"input\", MODE=\"0660\", OPTIONS+=\"static_node=uinput\"' | sudo tee /etc/udev/rules.d/60-edgehop.rules"
	echo "  sudo udevadm control --reload-rules && sudo udevadm trigger"
	echo
	echo "then log out and back in (or run edgehop with sudo)."
fi
echo
echo "Start it with:  $run"
