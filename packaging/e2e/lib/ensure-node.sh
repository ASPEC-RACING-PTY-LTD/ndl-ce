#!/bin/sh
# Source this file; do not execute it.
# Puts Node 24 and pnpm on PATH for Debian package builds.

NODE_VER=${NODE_VER:-v24.8.0}

node_major_ok() {
  ver=$(node -v 2>/dev/null || true)
  case "$ver" in
    v1[8-9].*|v[2-9][0-9].*)
      return 0
      ;;
  esac
  return 1
}

ensure_node() {
  if [ "$(id -u)" -eq 0 ]; then
    node_prefix=/usr/local
  else
    node_prefix=${HOME}/.local
  fi
  node_dir="${NODE_HOME:-$node_prefix/node-${NODE_VER}-linux-x64}"

  if command -v node >/dev/null 2>&1 && node_major_ok; then
    :
  elif [ -x "$node_dir/bin/node" ]; then
    export PATH="$node_dir/bin:$PATH"
  else
    mkdir -p "$node_prefix"
    tarball="node-${NODE_VER}-linux-x64.tar.xz"
    tmp=$(mktemp -d)
    curl -fsSL "https://nodejs.org/dist/${NODE_VER}/${tarball}" -o "$tmp/$tarball"
    tar -C "$node_prefix" -xJf "$tmp/$tarball"
    rm -rf "$tmp"
    export PATH="$node_dir/bin:$PATH"
  fi
  if ! command -v node >/dev/null 2>&1 || ! node_major_ok; then
    echo "node 18+ is required to build ndl-ui" >&2
    exit 1
  fi
  if ! command -v pnpm >/dev/null 2>&1; then
    if ! command -v corepack >/dev/null 2>&1; then
      echo "corepack is required to provide pnpm" >&2
      exit 1
    fi
    corepack enable
    corepack prepare pnpm@10.10.0 --activate
  fi
  if ! command -v pnpm >/dev/null 2>&1; then
    echo "pnpm is required to build ndl-ui" >&2
    exit 1
  fi
  node -v
  pnpm -v
}
