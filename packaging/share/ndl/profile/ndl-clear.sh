# Installed by No-dal. `clear` also removes the scrollback, whatever TERM the
# terminal reports: some SSH apps report a TERM whose terminfo has no E3
# (erase saved lines), so the screen cleared but scrolling up showed it all.
# `clear -x` still keeps the scrollback.
clear() {
  case "${1:-}" in
    -x|-V|-T*) command clear "$@"; return ;;
  esac
  printf '\033[H\033[2J\033[3J'
}
