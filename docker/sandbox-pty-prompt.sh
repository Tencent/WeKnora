# WeKnora interactive PTY prompt: classic \u@\h:\W\$
# (user@host:last-path-segment, # for root / $ otherwise).
#
# \W is the last directory only (e.g. /opt/weknora/skills → skills),
# so deep cwd does not blow the prompt width.
#
# Bold green / blue (01;32, 01;34) map onto theme.css brand green in xterm.
# Root uses the same green as a normal user — not Debian's warning red.
#
# Sourced from profile.d and bashrc. E2B's template provisioner later
# appends PS1='\w $ '; PROMPT_COMMAND reapplies this prompt so that cannot
# stick. No-op for non-bash (Debian /etc/profile is also read by dash).

[ -n "${BASH_VERSION-}" ] || return 0

weknora_set_pty_prompt() {
	PS1='\[\033[01;32m\]\u@\h\[\033[00m\]:\[\033[01;34m\]\W\[\033[00m\]\$ '
}

weknora_set_pty_prompt

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
export PS1 PROMPT_COMMAND
