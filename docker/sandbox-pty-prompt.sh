# WeKnora interactive PTY prompt: classic \u@\h:\W\$
# (user@host:last-path-segment, # for root / $ otherwise).
#
# \W is the last directory only (e.g. /opt/weknora/skills → skills),
# so deep cwd does not blow the prompt width.
#
# Bold green (01;32) is user@host; bold blue (01;34) is the path — the
# usual Debian colors, so ls --color can keep directories blue and
# executables green. Root uses the same green as a normal user.
#
# Also enables Debian-style interactive aliases (ll, la, l, colored ls/grep).
#
# Sourced from profile.d and bashrc. E2B's template provisioner later
# appends PS1='\w $ '; PROMPT_COMMAND reapplies this prompt so that cannot
# stick. No-op for non-bash (Debian /etc/profile is also read by dash).

[ -n "${BASH_VERSION-}" ] || return 0

weknora_set_pty_prompt() {
	PS1='\[\033[01;32m\]\u@\h\[\033[00m\]:\[\033[01;34m\]\W\[\033[00m\]\$ '
}

weknora_set_pty_prompt

# Debian/Ubuntu interactive shortcuts. Aliases only apply in interactive
# shells, so skill scripts still see the real ls/grep.
case "$-" in
*i*)
	if command -v dircolors >/dev/null 2>&1; then
		eval "$(dircolors -b)"
	fi
	alias ls='ls --color=auto'
	alias grep='grep --color=auto'
	alias fgrep='fgrep --color=auto'
	alias egrep='egrep --color=auto'
	alias ll='ls -alF'
	alias la='ls -A'
	alias l='ls -CF'
	;;
esac

case ";${PROMPT_COMMAND-};" in
	*weknora_set_pty_prompt*) ;;
	*)
		if [ -n "${PROMPT_COMMAND-}" ]; then
			PROMPT_COMMAND="weknora_set_pty_prompt; ${PROMPT_COMMAND}"
		else
			PROMPT_COMMAND="weknora_set_pty_prompt"
		fi
		;;
esac
# PS1 and PROMPT_COMMAND stay shell-local. Exporting PS1 makes child
# processes look interactive (`[ -z "$PS1" ]`); exporting PROMPT_COMMAND
# without `export -f weknora_set_pty_prompt` breaks nested bash.
