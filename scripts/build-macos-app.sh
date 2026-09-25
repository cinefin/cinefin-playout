#!/bin/bash
# Build the self-contained macOS .app of the desktop flavour (system tray) with a
# bundled mpv, and zip it into dist/ ready to attach to a Gitea release.
#
# macOS's tray needs native cgo (Cocoa), so this runs on a Mac (for the arch it
# builds — Apple Silicon Macs produce arm64, Intel Macs amd64). The release
# workflow covers the headless "service" darwin builds and the CGO-free
# linux/windows desktop builds; this script is the manual step for the darwin
# desktop asset. Unlike those, the .app bundles mpv so it is a true double-click
# app that needs no Homebrew.
#
# The agent's own UI links only the Cocoa system framework (for the tray), so it
# drags in no Homebrew dylibs. mpv does: the `mpv` binary is copied next to the
# agent (so config.ResolveMPVBinary finds it as a sibling) and its dylib closure
# (ffmpeg, libass, …) is pulled into Contents/Frameworks with the load commands
# rewritten, so the bundle runs on a Mac with no Homebrew at all.
#
# Requirements:  Xcode command line tools, Go, and `brew install mpv`.
#
# NOTE: this script is macOS-only and cannot be exercised in CI — verify on a Mac
# after changes (build, then open the .app, confirm the tray appears, "Open
# control panel…" opens /ui in the browser, and mpv plays).
#
# The bundle is ad-hoc signed (no Developer ID), so a downloaded copy is
# quarantined by Gatekeeper: first run is right-click → Open, or
# `xattr -d com.apple.quarantine "Cinefin Playout.app"`.
set -euo pipefail

cd "$(dirname "$0")/.."

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
case "$(uname -m)" in
  arm64) GOARCH_NAME=arm64 ;;
  x86_64) GOARCH_NAME=amd64 ;;
  *) echo "unsupported arch: $(uname -m)" >&2; exit 1 ;;
esac

APP_NAME="Cinefin Playout"
BUNDLE_ID="net.msilas.cinefin-playout"
BUILD=build/macos
APP="${BUILD}/${APP_NAME}.app"
MACOS="${APP}/Contents/MacOS"
FRAMEWORKS="${APP}/Contents/Frameworks"
RESOURCES="${APP}/Contents/Resources"
BIN="${MACOS}/cinefin-playout"
MPVBIN="${MACOS}/mpv"
LDFLAGS="-s -w -X github.com/cinefin/cinefin-playout/internal/version.Version=${VERSION}"

MPV_SRC="${MPV_SRC:-$(command -v mpv || true)}"
[ -n "${MPV_SRC}" ] || { echo "mpv not found — brew install mpv (or set MPV_SRC)" >&2; exit 1; }

echo "--- building ${APP_NAME}.app ${VERSION} (darwin/${GOARCH_NAME})"
rm -rf "${APP}"
mkdir -p "${MACOS}" "${FRAMEWORKS}" "${RESOURCES}" dist

CGO_ENABLED=1 go build -trimpath -tags ui -ldflags "${LDFLAGS}" \
  -o "${BIN}" ./cmd/cinefin-playout

# Bundle the mpv binary next to the agent (config.ResolveMPVBinary prefers a
# sibling "mpv"). Its dylib closure is bundled below.
cp "${MPV_SRC}" "${MPVBIN}"
chmod u+w "${MPVBIN}"

# --- Info.plist. LSUIElement: it's a tray agent — no Dock icon; the tray still
# shows fine.
/usr/bin/plutil -create xml1 "${APP}/Contents/Info.plist"
plist() { /usr/bin/plutil -insert "$1" -string "$2" "${APP}/Contents/Info.plist"; }
plist CFBundleExecutable      "cinefin-playout"
plist CFBundleIdentifier      "${BUNDLE_ID}"
plist CFBundleName            "${APP_NAME}"
plist CFBundlePackageType     "APPL"
plist CFBundleShortVersionString "${VERSION#v}"
plist CFBundleVersion         "${VERSION#v}"
plist CFBundleIconFile        "icon"
/usr/bin/plutil -insert LSUIElement -bool true "${APP}/Contents/Info.plist"
/usr/bin/plutil -insert NSHighResolutionCapable -bool true "${APP}/Contents/Info.plist"

# --- App icon, generated from the tray icon.
ICONSET="${BUILD}/icon.iconset"
rm -rf "${ICONSET}"; mkdir -p "${ICONSET}"
for size in 16 32 64 128 256 512; do
  sips -z "${size}" "${size}" internal/ui/icon.png \
    --out "${ICONSET}/icon_${size}x${size}.png" >/dev/null
done
iconutil -c icns "${ICONSET}" -o "${RESOURCES}/icon.icns"
rm -rf "${ICONSET}"

# --- Bundle the dylib closure of the mpv binary. Fixpoint copy: pull every
# Homebrew (or /usr/local) library mpv or an already-copied dylib links,
# resolving @rpath against the seeds' LC_RPATH entries, then rewrite all load
# commands to @executable_path/../Frameworks/<name>.
#
# sources.txt maps "<basename> <original path>" so @loader_path references in a
# copied dylib can still be resolved against where it came from.
SOURCES="${BUILD}/sources.txt"; : > "${SOURCES}"

# rpaths_of collects the LC_RPATH search paths declared in a Mach-O file.
rpaths_of() { otool -l "$1" | awk '/LC_RPATH/{r=1} r&&/path /{print $2; r=0}'; }
MPV_RPATHS="$(rpaths_of "${MPVBIN}" || true)"

deps_of() { # deps_of <file> — linked library paths, one per line
  otool -L "$1" | awk 'NR>1 {print $1}'
}

copy_dep() { # copy_dep <original path> — dereference symlinks into Frameworks
  local src="$1" base; base="$(basename "$1")"
  [ -e "${FRAMEWORKS}/${base}" ] && return 0
  cp -L "${src}" "${FRAMEWORKS}/${base}"
  chmod u+w "${FRAMEWORKS}/${base}"
  echo "${base} ${src}" >> "${SOURCES}"
  echo "    + ${base}"
}

resolve_rpath() { # resolve_rpath <@rpath/name> — first matching LC_RPATH hit
  local name="${1#@rpath/}" rp
  for rp in ${MPV_RPATHS}; do
    [ -e "${rp}/${name}" ] && { echo "${rp}/${name}"; return 0; }
  done
  return 1
}

echo "--- bundling mpv's dependencies"
changed=1
while [ "${changed}" = 1 ]; do
  changed=0
  for f in "${MPVBIN}" "${FRAMEWORKS}"/*.dylib; do
    [ -e "${f}" ] || continue
    while IFS= read -r dep; do
      case "${dep}" in
        /opt/homebrew/*|/usr/local/*) ;;
        @rpath/*)
          dep="$(resolve_rpath "${dep}")" || { echo "cannot resolve ${dep}" >&2; continue; }
          ;;
        @loader_path/*)
          orig="$(awk -v b="$(basename "${f}")" '$1==b {print $2}' "${SOURCES}")"
          [ -n "${orig}" ] || { echo "cannot resolve ${dep} in ${f}" >&2; exit 1; }
          dep="$(cd "$(dirname "${orig}")" && cd "$(dirname "${dep#@loader_path/}")" && pwd)/$(basename "${dep}")"
          ;;
        *) continue ;;
      esac
      if [ ! -e "${FRAMEWORKS}/$(basename "${dep}")" ]; then
        copy_dep "${dep}"
        changed=1
      fi
    done < <(deps_of "${f}")
  done
done

echo "--- rewriting load commands"
for f in "${MPVBIN}" "${FRAMEWORKS}"/*.dylib; do
  [ -e "${f}" ] || continue
  [ "${f}" != "${MPVBIN}" ] && install_name_tool -id \
    "@executable_path/../Frameworks/$(basename "${f}")" "${f}" 2>/dev/null
  while IFS= read -r dep; do
    base="$(basename "${dep}")"
    case "${dep}" in
      /opt/homebrew/*|/usr/local/*|@rpath/*|@loader_path/*)
        [ -e "${FRAMEWORKS}/${base}" ] || continue
        install_name_tool -change "${dep}" \
          "@executable_path/../Frameworks/${base}" "${f}" 2>/dev/null
        ;;
    esac
  done < <(deps_of "${f}")
done
rm -f "${SOURCES}"

# --- Verify nothing still points outside the bundle or the OS.
echo "--- verifying"
bad=0
for f in "${BIN}" "${MPVBIN}" "${FRAMEWORKS}"/*.dylib; do
  [ -e "${f}" ] || continue
  while IFS= read -r dep; do
    case "${dep}" in
      /usr/lib/*|/System/*|@executable_path/*) ;;
      *) echo "UNRESOLVED in $(basename "${f}"): ${dep}" >&2; bad=1 ;;
    esac
  done < <(deps_of "${f}")
done
[ "${bad}" = 0 ] || exit 1

# install_name_tool invalidates code signatures; re-sign everything ad-hoc
# (arm64 refuses to run unsigned binaries at all).
echo "--- ad-hoc signing"
for f in "${FRAMEWORKS}"/*.dylib; do codesign --force -s - "${f}" >/dev/null 2>&1; done
codesign --force -s - "${MPVBIN}"
codesign --force -s - "${BIN}"
codesign --force -s - "${APP}"

# Smoke test: the bundled mpv resolves its dylibs from the bundle.
echo "--- smoke test: agent → $("${BIN}" --version); mpv → $("${MPVBIN}" --version | head -n1)"

NAME="cinefin-playout-${VERSION}-darwin-${GOARCH_NAME}-desktop"
ditto -c -k --keepParent "${APP}" "dist/${NAME}.zip"
echo "--- done: dist/${NAME}.zip ($(du -h "dist/${NAME}.zip" | cut -f1 | tr -d ' '))"
echo "attach it to the release next to the CI-built service archives"
