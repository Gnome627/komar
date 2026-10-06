#!/bin/bash

# komar installer for Arch and Debian-based distros, with Omarchy integration.
#
#   ./install.sh                 install (build from this checkout)
#   curl -fsSL https://raw.githubusercontent.com/gnome627/komar/main/install.sh | bash
#   ./install.sh --uninstall     remove komar and everything the installer added
#
# Options:
#   --prefix=DIR      where to install (default: ~/.local, binary in DIR/bin)
#   --from-source     always build from source, even when a release binary exists
#   --no-hotkey       don't add the SUPER+SHIFT+K keybinding
#   --no-menu         don't add komar to the Omarchy menu
#   --no-deps         don't install kubectl, git, curl
#   --uninstall       remove komar

set -euo pipefail

REPO="gnome627/komar"
PREFIX="${KOMAR_PREFIX:-$HOME/.local}"
CACHE_DIR="${XDG_CACHE_HOME:-$HOME/.cache}/komar"
DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}"
CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}"

FROM_SOURCE=false
WANT_HOTKEY=true
WANT_MENU=true
WANT_DEPS=true
UNINSTALL=false

BLOCK_BEGIN=">>> komar >>>"
BLOCK_END="<<< komar <<<"

# --- output ---------------------------------------------------------------

if [[ -t 1 ]]; then
  C_ACCENT=$'\e[1;34m' C_OK=$'\e[32m' C_WARN=$'\e[33m' C_ERR=$'\e[31m' C_DIM=$'\e[2m' C_RESET=$'\e[0m'
else
  C_ACCENT="" C_OK="" C_WARN="" C_ERR="" C_DIM="" C_RESET=""
fi

step() { echo "${C_ACCENT}::${C_RESET} $*"; }
ok() { echo "   ${C_OK}✓${C_RESET} $*"; }
warn() { echo "   ${C_WARN}!${C_RESET} $*"; }
die() {
  echo "${C_ERR}✗ $*${C_RESET}" >&2
  exit 1
}

for arg in "$@"; do
  case "$arg" in
  --prefix=*) PREFIX="${arg#--prefix=}" ;;
  --from-source) FROM_SOURCE=true ;;
  --no-hotkey) WANT_HOTKEY=false ;;
  --no-menu) WANT_MENU=false ;;
  --no-deps) WANT_DEPS=false ;;
  --uninstall) UNINSTALL=true ;;
  -h | --help)
    sed -n '3,16p' "$0" | sed 's/^# \{0,1\}//'
    exit 0
    ;;
  *) die "unknown option: $arg (see --help)" ;;
  esac
done

BIN_DIR="$PREFIX/bin"
BIN="$BIN_DIR/komar"

# --- system ---------------------------------------------------------------

SUDO=""
if (( EUID != 0 )); then
  SUDO="sudo"
fi

detect_distro() {
  DISTRO="other"
  if [[ -f /etc/os-release ]]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    case " ${ID:-} ${ID_LIKE:-} " in
    *" arch "* | *" archlinux "* | *" manjaro "* | *" endeavouros "*) DISTRO="arch" ;;
    *" debian "* | *" ubuntu "*) DISTRO="debian" ;;
    esac
  fi
}

detect_arch() {
  case "$(uname -m)" in
  x86_64 | amd64) GOARCH="amd64" ;;
  aarch64 | arm64) GOARCH="arm64" ;;
  *) die "unsupported CPU architecture: $(uname -m)" ;;
  esac
}

is_omarchy() {
  [[ -d $HOME/.local/share/omarchy ]] || command -v omarchy-launch-tui >/dev/null 2>&1
}

have() { command -v "$1" >/dev/null 2>&1; }

install_deps() {
  step "Dependencies"
  local missing=()
  for c in curl tar git; do
    have "$c" || missing+=("$c")
  done
  case "$DISTRO" in
  arch)
    have kubectl || missing+=("kubectl")
    if (( ${#missing[@]} > 0 )); then
      $SUDO pacman -S --needed --noconfirm "${missing[@]}"
    fi
    ;;
  debian)
    if (( ${#missing[@]} > 0 )); then
      $SUDO apt-get update -qq
      $SUDO apt-get install -y -qq ca-certificates "${missing[@]}"
    fi
    have kubectl || install_kubectl_binary
    ;;
  *)
    if (( ${#missing[@]} > 0 )); then
      warn "please install: ${missing[*]}"
    fi
    have kubectl || install_kubectl_binary
    ;;
  esac
  if have kubectl; then
    ok "kubectl $(kubectl version --client 2>/dev/null | head -1 | awk '{print $3}')"
  fi
}

# Debian's kubectl packages live in a separate apt repo; the static binary
# from dl.k8s.io is simpler and always current.
install_kubectl_binary() {
  local version tmp
  version=$(curl -fsSL https://dl.k8s.io/release/stable.txt)
  tmp=$(mktemp -d)
  curl -fsSL -o "$tmp/kubectl" "https://dl.k8s.io/release/$version/bin/linux/$GOARCH/kubectl"
  curl -fsSL -o "$tmp/kubectl.sha256" "https://dl.k8s.io/release/$version/bin/linux/$GOARCH/kubectl.sha256"
  if [[ "$(sha256sum "$tmp/kubectl" | awk '{print $1}')" != "$(cat "$tmp/kubectl.sha256")" ]]; then
    rm -rf "$tmp"
    die "kubectl checksum mismatch"
  fi
  install -Dm755 "$tmp/kubectl" "$BIN_DIR/kubectl"
  rm -rf "$tmp"
  ok "kubectl $version → $BIN_DIR/kubectl"
}

# --- getting the binary ---------------------------------------------------

script_dir() {
  local src="${BASH_SOURCE[0]:-}"
  if [[ -n $src && -f $src ]]; then
    cd "$(dirname "$src")" && pwd
  fi
}

version_ge() {
  [[ "$(printf '%s\n%s\n' "$2" "$1" | sort -V | head -1)" == "$2" ]]
}

# ensure_go makes a Go toolchain new enough for go.mod available as $GO,
# downloading one into the cache when the system Go is missing or older
# (Debian stable usually is).
ensure_go() {
  local need="$1" have_v
  if have go; then
    have_v=$(go env GOVERSION 2>/dev/null | sed 's/^go//')
    # Go 1.21+ fetches the toolchain go.mod asks for by itself
    # (GOTOOLCHAIN=auto), so any recent system Go will do.
    if [[ -n $have_v ]] && version_ge "$have_v" "1.21"; then
      GO="go"
      return
    fi
  fi
  if [[ $DISTRO == "arch" ]] && $WANT_DEPS; then
    $SUDO pacman -S --needed --noconfirm go
    GO="go"
    return
  fi
  local dir="$CACHE_DIR/go$need"
  if [[ ! -x $dir/bin/go ]]; then
    step "Downloading Go $need (only used to build komar)"
    mkdir -p "$dir"
    curl -fsSL "https://go.dev/dl/go$need.linux-$GOARCH.tar.gz" | tar -xz -C "$dir" --strip-components=1 ||
      die "could not download Go $need"
  fi
  GO="$dir/bin/go"
}

build_from_source() {
  local src="$1" need version out
  step "Building komar from source"
  need=$(sed -n 's/^go \([0-9.]*\).*/\1/p' "$src/go.mod" | head -1)
  ensure_go "$need"
  version=$(git -C "$src" describe --tags --always --dirty 2>/dev/null || echo dev)
  out=$(mktemp -d)
  (cd "$src" && CGO_ENABLED=0 GOTOOLCHAIN=auto "$GO" build -trimpath -ldflags "-s -w -X main.version=$version" -o "$out/komar" ./cmd/komar)
  install -Dm755 "$out/komar" "$BIN"
  rm -rf "$out"
  ok "komar $version → $BIN"
}

download_release() {
  local url="https://github.com/$REPO/releases/latest/download/komar-linux-$GOARCH.tar.gz" tmp
  tmp=$(mktemp -d)
  if curl -fsSL -o "$tmp/komar.tar.gz" "$url" 2>/dev/null && tar -xzf "$tmp/komar.tar.gz" -C "$tmp" 2>/dev/null && [[ -f $tmp/komar ]]; then
    install -Dm755 "$tmp/komar" "$BIN"
    rm -rf "$tmp"
    ok "komar (release) → $BIN"
    return 0
  fi
  rm -rf "$tmp"
  return 1
}

get_binary() {
  local src
  src=$(script_dir)
  if [[ -n $src && -f $src/go.mod && -d $src/cmd/komar ]]; then
    build_from_source "$src"
    ASSETS="$src/assets"
    return
  fi
  if ! $FROM_SOURCE; then
    step "Downloading komar"
    if download_release; then
      fetch_assets
      return
    fi
    warn "no release binary for linux/$GOARCH yet, building from source"
  fi
  local tmp
  tmp=$(mktemp -d)
  git clone --depth 1 "https://github.com/$REPO.git" "$tmp/komar" >/dev/null 2>&1 || die "could not clone $REPO"
  build_from_source "$tmp/komar"
  mkdir -p "$CACHE_DIR/assets"
  cp -r "$tmp/komar/assets/." "$CACHE_DIR/assets/"
  ASSETS="$CACHE_DIR/assets"
  rm -rf "$tmp"
}

fetch_assets() {
  ASSETS="$CACHE_DIR/assets"
  mkdir -p "$ASSETS"
  curl -fsSL -o "$ASSETS/komar.svg" "https://raw.githubusercontent.com/$REPO/main/assets/komar.svg" || true
}

# --- config blocks --------------------------------------------------------

# add_block FILE COMMENT TEXT appends TEXT between komar markers, replacing
# an earlier komar block so re-running the installer is safe.
add_block() {
  local file="$1" c="$2" text="$3"
  remove_block "$file" "$c"
  mkdir -p "$(dirname "$file")"
  printf '\n%s %s\n%s\n%s %s\n' "$c" "$BLOCK_BEGIN" "$text" "$c" "$BLOCK_END" >>"$file"
}

remove_block() {
  local file="$1" c="$2"
  [[ -f $file ]] || return 0
  if grep -qF -- "$c $BLOCK_BEGIN" "$file"; then
    local tmp
    tmp=$(mktemp)
    # Drop the block and the blank line add_block put in front of it.
    awk -v b="$c $BLOCK_BEGIN" -v e="$c $BLOCK_END" '
      $0 == b { skip = 1; if (held && line != "") print line; held = 0; next }
      $0 == e { skip = 0; next }
      skip { next }
      { if (held) print line; line = $0; held = 1 }
      END { if (held) print line }
    ' "$file" >"$tmp"
    cat "$tmp" >"$file"
    rm -f "$tmp"
  fi
}

# --- desktop entry --------------------------------------------------------

launch_command() {
  if is_omarchy; then
    echo "omarchy-launch-or-focus-tui komar"
  elif have xdg-terminal-exec; then
    echo "xdg-terminal-exec $BIN"
  else
    local t
    for t in ptyxis kgx gnome-terminal konsole foot alacritty ghostty kitty xfce4-terminal x-terminal-emulator xterm; do
      if have "$t"; then
        case "$t" in
        ptyxis | kgx | gnome-terminal) echo "$t -- $BIN" ;;
        foot | kitty) echo "$t $BIN" ;;
        xfce4-terminal) echo "$t -x $BIN" ;;
        *) echo "$t -e $BIN" ;;
        esac
        return
      fi
    done
    echo ""
  fi
}

install_desktop() {
  step "Launcher"
  local icon_dir="$DATA_DIR/icons/hicolor/scalable/apps" desktop="$DATA_DIR/applications/komar.desktop" exec_line terminal="false"
  if [[ -f ${ASSETS:-}/komar.svg ]]; then
    install -Dm644 "$ASSETS/komar.svg" "$icon_dir/komar.svg"
    have gtk-update-icon-cache && gtk-update-icon-cache -q "$DATA_DIR/icons/hicolor" >/dev/null 2>&1 || true
  fi
  exec_line=$(launch_command)
  if [[ -z $exec_line ]]; then
    exec_line="$BIN"
    terminal="true"
  fi
  mkdir -p "$(dirname "$desktop")"
  cat >"$desktop" <<EOF
[Desktop Entry]
Version=1.0
Type=Application
Name=komar
GenericName=Kubernetes
Comment=Kubernetes in the terminal, Omarchy style
Comment[ru]=Kubernetes в терминале в стиле Omarchy
Exec=$exec_line
Terminal=$terminal
Icon=komar
Categories=Development;System;
Keywords=kubernetes;k8s;kubectl;pods;cluster;
StartupNotify=false
EOF
  have update-desktop-database && update-desktop-database -q "$DATA_DIR/applications" >/dev/null 2>&1 || true
  ok "app launcher entry (search for \"komar\")"
}

# --- Omarchy ---------------------------------------------------------------

omarchy_hotkey() {
  local lua="$CONFIG_DIR/hypr/bindings.lua" conf="$CONFIG_DIR/hypr/bindings.conf"
  if [[ -f $lua ]]; then
    # Omarchy 4: bindings are Lua.
    add_block "$lua" "--" 'o.bind("SUPER + SHIFT + K", "Kubernetes", { tui = "komar", focus = true })'
    ok "SUPER + SHIFT + K → komar ($lua)"
  elif [[ -f $conf ]]; then
    # Omarchy 3: Hyprland conf syntax.
    add_block "$conf" "#" "bindd = SUPER SHIFT, K, Kubernetes, exec, omarchy-launch-or-focus-tui komar"
    ok "SUPER + SHIFT + K → komar ($conf)"
  else
    warn "no Hyprland bindings file found, skipping hotkey"
    return
  fi
  if [[ -n ${HYPRLAND_INSTANCE_SIGNATURE:-} ]] && have hyprctl; then
    hyprctl reload >/dev/null 2>&1 || true
  fi
}

omarchy_menu() {
  local jsonc="$CONFIG_DIR/omarchy/extensions/omarchy-menu.jsonc"
  local menush="$CONFIG_DIR/omarchy/extensions/menu.sh"
  if [[ -f $jsonc ]] || have omarchy-shell; then
    # Omarchy 4: the Quickshell menu reads a JSONC extension file.
    omarchy_menu_jsonc "$jsonc"
    ok "Omarchy menu → Kubernetes ($jsonc)"
    omarchy-menu refresh >/dev/null 2>&1 || true
  else
    # Omarchy 3: the walker menu sources extensions/menu.sh last, so we wrap
    # its functions to add a top-level "Kubernetes" entry.
    add_block "$menush" "#" "$(cat <<'EOF'
eval "$(declare -f menu | sed '1s/^menu/komar_orig_menu/')"
menu() {
  if [[ $1 == "Go" ]]; then
    komar_orig_menu "$1" "$2\n󱃾  Kubernetes" "${@:3}"
  else
    komar_orig_menu "$@"
  fi
}
eval "$(declare -f go_to_menu | sed '1s/^go_to_menu/komar_orig_go_to_menu/')"
go_to_menu() {
  case "${1,,}" in
  *kubernetes*) omarchy-launch-or-focus-tui komar ;;
  *) komar_orig_go_to_menu "$1" ;;
  esac
}
EOF
)"
    ok "Omarchy menu → Kubernetes ($menush)"
  fi
}

# JSONC has no tooling on a stock system, so the entry goes in as text:
# right after the opening brace, with a comma only when other entries
# follow it.
omarchy_menu_jsonc() {
  local file="$1" entry has_keys tmp
  entry='  "komar": {"icon": "󱃾", "label": "Kubernetes", "description": "komar — Kubernetes TUI", "action": "omarchy-launch-or-focus-tui komar"}'
  mkdir -p "$(dirname "$file")"
  [[ -f $file ]] || echo "{}" >"$file"
  remove_block "$file" "//"
  has_keys=false
  if sed 's://.*$::' "$file" | grep -q '"'; then
    has_keys=true
  fi
  if $has_keys; then
    entry+=","
  fi
  tmp=$(mktemp)
  awk -v b="// $BLOCK_BEGIN" -v e="// $BLOCK_END" -v entry="$entry" '
    !done && /\{/ {
      i = index($0, "{")
      print substr($0, 1, i)
      print b
      print entry
      print e
      rest = substr($0, i + 1)
      if (rest != "") print rest
      done = 1
      next
    }
    { print }
  ' "$file" >"$tmp"
  cat "$tmp" >"$file"
  rm -f "$tmp"
}

# --- other desktops -------------------------------------------------------

other_hotkey() {
  local launch
  launch=$(launch_command)
  if [[ -z $launch ]]; then
    warn "no terminal emulator found; set a hotkey for: <terminal> -e komar"
    return
  fi
  if [[ -f $CONFIG_DIR/hypr/hyprland.conf ]]; then
    add_block "$CONFIG_DIR/hypr/hyprland.conf" "#" "bind = SUPER SHIFT, K, exec, $launch"
    ok "Hyprland: SUPER + SHIFT + K → komar"
    [[ -n ${HYPRLAND_INSTANCE_SIGNATURE:-} ]] && have hyprctl && hyprctl reload >/dev/null 2>&1 || true
  elif have gsettings && [[ ${XDG_CURRENT_DESKTOP:-} == *GNOME* ]]; then
    gnome_hotkey "$launch"
  else
    warn "add a hotkey in your desktop settings for: $launch"
  fi
}

GNOME_KEY_PATH="/org/gnome/settings-daemon/plugins/media-keys/custom-keybindings/komar/"
GNOME_SCHEMA="org.gnome.settings-daemon.plugins.media-keys"

gnome_hotkey() {
  local launch="$1" list
  list=$(gsettings get $GNOME_SCHEMA custom-keybindings)
  if [[ $list != *"$GNOME_KEY_PATH"* ]]; then
    if [[ $list == "@as []" || $list == "[]" ]]; then
      list="['$GNOME_KEY_PATH']"
    else
      list="${list%]}, '$GNOME_KEY_PATH']"
    fi
    gsettings set $GNOME_SCHEMA custom-keybindings "$list"
  fi
  local s="$GNOME_SCHEMA.custom-keybinding:$GNOME_KEY_PATH"
  gsettings set "$s" name "komar"
  gsettings set "$s" command "$launch"
  gsettings set "$s" binding "<Super><Shift>k"
  ok "GNOME: Super + Shift + K → komar"
}

gnome_unhotkey() {
  have gsettings || return 0
  local list
  list=$(gsettings get $GNOME_SCHEMA custom-keybindings 2>/dev/null) || return 0
  [[ $list == *"$GNOME_KEY_PATH"* ]] || return 0
  list=$(echo "$list" | sed "s#, '$GNOME_KEY_PATH'##; s#'$GNOME_KEY_PATH', ##; s#'$GNOME_KEY_PATH'##")
  [[ $list == "[]" ]] && list="@as []"
  gsettings set $GNOME_SCHEMA custom-keybindings "$list"
  gsettings reset-recursively "$GNOME_SCHEMA.custom-keybinding:$GNOME_KEY_PATH" 2>/dev/null || true
}

# --- uninstall ------------------------------------------------------------

uninstall() {
  step "Removing komar"
  rm -f "$BIN" "$DATA_DIR/applications/komar.desktop" "$DATA_DIR/icons/hicolor/scalable/apps/komar.svg"
  remove_block "$CONFIG_DIR/hypr/bindings.lua" "--"
  remove_block "$CONFIG_DIR/hypr/bindings.conf" "#"
  remove_block "$CONFIG_DIR/hypr/hyprland.conf" "#"
  remove_block "$CONFIG_DIR/omarchy/extensions/menu.sh" "#"
  remove_block "$CONFIG_DIR/omarchy/extensions/omarchy-menu.jsonc" "//"
  gnome_unhotkey
  have omarchy-menu && omarchy-menu refresh >/dev/null 2>&1 || true
  [[ -n ${HYPRLAND_INSTANCE_SIGNATURE:-} ]] && have hyprctl && hyprctl reload >/dev/null 2>&1 || true
  ok "removed (config in ~/.config/komar and history in ~/.local/state/komar were kept)"
}

# --- main -----------------------------------------------------------------

main() {
  detect_distro
  detect_arch
  if $UNINSTALL; then
    uninstall
    return
  fi
  echo "${C_ACCENT}komar${C_RESET} ${C_DIM}— Kubernetes, Omarchy style ($DISTRO/$GOARCH)${C_RESET}"
  mkdir -p "$BIN_DIR"
  if $WANT_DEPS; then
    install_deps
  fi
  get_binary
  install_desktop

  if is_omarchy; then
    step "Omarchy"
    $WANT_HOTKEY && omarchy_hotkey
    $WANT_MENU && omarchy_menu
  elif $WANT_HOTKEY; then
    step "Hotkey"
    other_hotkey
  fi

  case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) warn "$BIN_DIR is not in PATH; add it to your shell profile" ;;
  esac
  echo
  echo "${C_OK}Done.${C_RESET} Run ${C_ACCENT}komar${C_RESET}$(is_omarchy && $WANT_HOTKEY && echo " or press SUPER + SHIFT + K")."
}

main
