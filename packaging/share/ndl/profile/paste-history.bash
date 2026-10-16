# Installed by No-dal. Sourced by Bash only.
# A multi-line paste is kept in history as one entry, so the up arrow brings
# the whole block back instead of its lines one by one.
if [ -n "${BASH_VERSION:-}" ] && [ -n "${PS1:-}" ] && [ -z "${__ndl_paste_hook:-}" ]; then
  __ndl_paste_hook=1
  shopt -s cmdhist lithist 2>/dev/null
  __ndl_hist_last() {
    local l
    l=$(HISTTIMEFORMAT= history 1)
    l=${l#"${l%%[![:space:]]*}"}
    printf '%s' "${l%%[!0-9]*}"
  }
  __ndl_join_paste() {
    local now count line joined="" re='^ *[0-9]+  (.*)$'
    now=$(__ndl_hist_last)
    # More than one entry since the last prompt means one input held several
    # lines: a paste. (HISTCMD and fc lag behind after a paste, so the
    # numbers come from the history listing itself.)
    if [ -n "$now" ] && [ -n "${__ndl_hist_seen:-}" ]; then
      count=$((now - __ndl_hist_seen))
      if [ "$count" -gt 1 ] && [ "$count" -le 200 ]; then
        while IFS= read -r line; do
          if [[ $line =~ $re ]]; then
            joined+=${joined:+$'\n'}${BASH_REMATCH[1]}
          else
            joined+=$'\n'${line#$'\t'}
          fi
        done < <(HISTTIMEFORMAT= history "$count")
        if [ -n "$joined" ] && history -d "$((__ndl_hist_seen + 1))-$now" 2>/dev/null; then
          history -s "$joined"
          now=$(__ndl_hist_last)
        fi
      fi
    fi
    __ndl_hist_seen=$now
  }
  PROMPT_COMMAND="__ndl_join_paste${PROMPT_COMMAND:+; $PROMPT_COMMAND}"
fi
