#!/bin/sh
# Persistent development APT signing. Source this file; do not execute it.
# Generate a key only when none exists. Never rotate a live key.
# Always publish $OUT/gpg from the secret key that actually signs.

sign_repo_home() {
  if [ -n "${SIGNING_HOME:-}" ]; then
    printf '%s\n' "$SIGNING_HOME"
    return
  fi
  if [ -d "${OUT:?}/signing/gnupg" ]; then
    printf '%s\n' "$OUT/signing/gnupg"
    return
  fi
  if [ -d "$OUT/gnupg" ]; then
    printf '%s\n' "$OUT/gnupg"
    return
  fi
  printf '%s\n' "$OUT/signing/gnupg"
}

ensure_signing_key() {
  SIGNING_HOME=$(sign_repo_home)
  export SIGNING_HOME
  mkdir -p "$SIGNING_HOME"
  chmod 700 "$SIGNING_HOME"
  export GNUPGHOME="$SIGNING_HOME"

  if gpg --list-secret-keys --with-colons 2>/dev/null | grep -q '^sec:'; then
    echo "Using persistent development repo signing key in $SIGNING_HOME"
    gpg --list-secret-keys --keyid-format LONG
    return
  fi

  echo "Creating the persistent development repo signing key (once) in $SIGNING_HOME"
  gpg --batch --pinentry-mode loopback --passphrase '' --quick-gen-key \
    "No-dal Test Repo <dev@no-dal.com>" rsa2048 default never
  gpg --list-secret-keys --keyid-format LONG
}

publish_public_key() {
  gpg --export --armor > "${OUT:?}/gpg"
  chmod a+r "$OUT/gpg"
  echo "Published $OUT/gpg from the signing secret key:"
  gpg --show-keys "$OUT/gpg"
}

sign_release() {
  release_dir=${1:?release directory}
  ensure_signing_key
  rm -f "$release_dir/InRelease" "$release_dir/Release.gpg"
  gpg --batch --yes --clearsign -o "$release_dir/InRelease" "$release_dir/Release"
  gpg --batch --yes --detach-sign -o "$release_dir/Release.gpg" "$release_dir/Release"
  publish_public_key
}
