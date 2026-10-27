---
name: agent-browser
description: Use when the task needs a real web browser inside the sandbox - open a page, click, fill forms, take screenshots, extract or scrape data, test a web app, or use a site that needs a login. Drives the preinstalled agent-browser CLI; ask the user with ask_user whether to run visibly (headed) or invisibly (headless) before the first browser command. This is NOT the local_browser tool, which controls the user's own Chrome on their computer.
---

# agent-browser (browser inside the sandbox)

The CLI is already installed. Do not install it again, do not run
`agent-browser install`, and never install a browser yourself: the sandbox
pins its own Google Chrome. Run it with shell_exec and without skill_name.
Every call is one command; the browser stays open between calls.

## Start here

This file is only the map. Load the real usage guide from the CLI itself, so
the instructions always match the installed version:

```sh
agent-browser skills get core        # workflows, common patterns, troubleshooting
agent-browser skills get core --full # adds the full command reference
agent-browser skills list            # specialized guides bundled with the CLI
```

For quick tasks the loop below is enough; read `core` before anything harder
(waits, iframes, multiple tabs, downloads, auth flows).

## Basic loop

```sh
agent-browser open https://example.com   # no --headed: runs headless
agent-browser snapshot -i          # interactive elements as @e1, @e2, ...
agent-browser click @e2
agent-browser fill @e3 "text to type"
agent-browser screenshot /workspace/output/page.png
agent-browser close                # closes the browser; the screen stays
```

Run `snapshot -i` again after every page change: refs from an older snapshot
go stale. `agent-browser --help` lists every other command.

## Screens

Before the first browser command of a task, ask the user with `ask_user`
whether to run visibly or invisibly. Visible means adding `--headed` to the
launch (the browser window appears on their Desktop tab); invisible means
leaving it off (headless). Apply the choice to the whole task; do not re-ask
for every command. If the user does not care, headless. A captcha, 2FA code
or password still needs the headed window: ask the user to type it there,
never in the chat.

## Rules

- Do not pass `--session`, `--profile`, `--cdp`, `--auto-connect`, `--state`
  or `--executable-path`. The sandbox supplies the profile, the session and
  the browser; those flags are discarded.
- Do not set DISPLAY, and do not target display :0. That screen belongs to
  other sessions.
- Close the browser only with `agent-browser close`. Never `pkill`/`kill`
  chrome or agent-browser processes: other sessions' browsers run on this
  machine too, and `agent-browser close` only ever touches this session's
  own browser.
- Cookie state is saved in a machine-level seed and restored before startup
  navigation. Live browsers mirror missing cookies and localStorage keys;
  existing keys are not overwritten. A receiving page may reload once idle.
  localStorage is shared from open pages, not persisted in the cookie seed.
  Cookie and password database files are shared; history, cache and IndexedDB
  stay in each profile. Signing out may affect shared login state. Windows
  remain on this session's screen. Never print cookie, password or token files.
