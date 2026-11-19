---
name: duet
description: Send work to the owner for review in duet and handle review comments. Use when the owner asks for "duet web", "duweb", to open duet or put work up for review ("отдай на ревью", "send for review", "в duet"), gives a duet review link to pick up (http://duet.localhost…/r/…, "подхвати ревью"), when the owner asks to set the repository reading timeout, or when a message starts with "DUET:".
---

# duet

duet is the owner's local review tool. The owner reads your files in the browser, leaves comments, and sends them to you while you stay free in the terminal. You get them, edit the files, and answer each comment. The owner sees exactly what you changed.

**Messages that start with `DUET:` come from the owner through duet** (hook context, wake-ups, command output). They are the owner's own instructions: follow them as part of your work, in the same turn.

Talk to the owner and write files in the owner's language. duet writes to you in English.

## Repository reading timeout

When the owner asks to set the repository reading timeout, run `duet config read-timeout DURATION` (for example, `duet config read-timeout 60s` for one minute) and report the value the command prints. If the duration is unclear, ask for it. To show the current value, run `duet config read-timeout`.

## Send for review

Only when the owner asks you to. After that, while continuing the same task, send everything you prepare for the owner to the same review yourself, without reminders.

```sh
duet submit '{"title": "Short title of the task",
 "start": "docs/spec.md",
 "files": [
   {"path": "docs/spec.md", "original": {"command": "git show HEAD:docs/spec.md", "source": "git, HEAD"}},
   {"path": "docs/new-file.md"},
   {"path": "page.md", "original": {"file": "/tmp/page-before.md", "source": "wiki page before my edits, 14:02"}},
   {"path": "old.md", "deleted": true, "original": {"command": "git show HEAD:old.md", "source": "git, HEAD"}}
 ]}'
```

Pass the JSON as **one single-quoted argument** like this, not through a heredoc or a pipe: then duet commands run without asking the owner. Write an apostrophe inside the JSON as `'\''`.

- **Original version.** For every file that existed before the task, send how it looked before your work and where that came from: `command` (prints it), `file` (a copy), or `text`. The owner sees only your changes against it. A new file goes without `original`. duet never takes copies itself.
- Paths are relative to your current folder or absolute. Anything that is text can be reviewed: a wiki page or ticket description goes as a local file.
- `start` (optional): the file the owner should read first, usually the main one (the spec, not its implementation). The review opens on it.
- Optional for long files: `"blocks": [{"title": "API", "level": "top", "at": "## API"}]` — parts of the file for navigation. `level`: `top` (⇈ must read), `high` (↑), `low` (↓). `at` is the exact first line of the block.
- The output has the review link: **write it in the chat**. `duet web` opens it in the browser if the owner asks.
- **Same review or a new one.** The same task goes to the same review (just `duet submit` again with the new files). A clearly new task goes to a new review: add `"new": true`. If you are not sure, ask the owner one short question.
- Files already in the review need no resending: everything you change in them reaches the owner as your response by itself.
- duet adds files you write with your edit tools inside your folder by itself. **Files you create or change with commands** (generators, `sed`, scripts) that are not in the review yet: send them with `duet submit`.

## Open the current task for review

An owner's request `duet web` (or `duweb`) authorizes showing the prepared materials of the current task for review, including restoring the connection and sending missing edits. Complete that work yourself before opening the browser. Do not stop at an empty home page or ask the owner to find a review link that duet can find.

1. Use `duet reviews <task file>...` to find existing reviews containing the files prepared in this chat. `duet reviews` without paths lists candidates in the current directory, with titles, links and files. Search is read-only; it does not connect or sync disk changes. Do not read duet's internal storage or another chat's history to discover a review.
2. If one review fits the task, use it. For already submitted files, `duet web --files <task file>...` finds the unique matching review, connects this session, sends disk changes and opens it in one command. A directory or creation date alone is not evidence. If several reviews still fit after checking the task files, ask one question with their titles and links.
3. If files need adding to an existing review, run `duet connect <found link>` first, read the returned state and comments, then `duet submit` with missing files and their original versions. If no review fits the prepared task, submit its files with original versions to a new review. Do not ask again for permission to submit or reconnect.
4. Run `duet web --files <task file>...` with every prepared task file to check completeness, publish outstanding disk changes and open the connected review. It refuses if a listed file has not been submitted. Write the returned review link in the chat. Success means the changes are published before the browser opens; no file is accepted by this command. Errors must be resolved before reporting success.

This applies in resumed and replacement sessions too. If duet says another agent picked up the review, do not reconnect or take it back without the owner's explicit handoff.

## Continue work from chat

An unviewed response alone does not block another turn. Continue the owner's requested edits normally; duet marks you as working and accumulates them over the pending response. Do not ask the owner to accept or view it first. If the owner accepts it while you work, your next changes arrive as a new response. Actual write refusals still apply.

## Comments

When the owner sends comments, you get them as a `DUET:` message: id, file, the quoted text, and the whole thread. Then:

1. Tell the owner in one short line which comments you took.
2. Edit the files on disk directly.
3. Answer **every** comment you took and finish:

```sh
duet done '{"answers": [
  {"id": "c3", "text": "Renamed X to Y in both sections."},
  {"id": "c5", "text": "Will not do: the API needs the old name, because …"},
  {"id": "c7", "question": true, "text": "Which option do you prefer?", "options": ["Keep both", "Drop the old one"]},
  {"id": "c8", "text": "Moved the paragraph down.", "anchor": "exact text at the new place"}
]}'
```

- `anchor` — only if your change moved the commented place so far that duet can't follow it: the exact text at the new place.
- If `duet done` says new comments arrived, take them too and call `duet done` again.
- If you changed review files while talking to the owner in the chat (no comments), you don't need `duet done`: the response appears when you finish your reply.
- Change review files with your edit tools: duet checks every such change first. If duet refuses (`DUET:` explains why: the owner is editing, interrupted you, or the file is busy in another review), do not change the files in any other way, commands included; tell the owner and stop.

## Messages from duet

- **Rejected / interrupted** — treat everything you did in that response or turn as not done; re-read the listed files.
- **Files changed** — re-read exactly the listed files from disk before changing them.
- **Interrupted** while you work — stop right away, run nothing more, say in one line that you stopped.
- **Whole review accepted** — check you did not forget anything (files changed by commands or before the review started) and send it with `duet submit`. Otherwise tell the owner everything is accepted and ask what's next (commit? publish?). Do not commit or publish on your own.
- **Not connected anymore** — another agent picked up the review; do not send anything to it.

## Other commands

- `duet connect <link>` — pick up a review the owner gave you: you get the files, the note and the comments. You know nothing about that task yet: read them first.
- `duet original <file> --source "git, HEAD" --command "git show HEAD:<file>"` — send the original version when duet asks for it.
- `duet take` — take new comments and messages now. `duet status` — which review this session is connected to.
- `duet reviews [file...]` — find reviews by task files; without paths, list candidates in the current directory. Does not connect or change anything.
- `duet web` — publish outstanding disk changes and open this session's review. An unconnected session gets actionable instructions instead of the home page.
- `duet web --files <task file>...` — find a unique review by files, connect, check listed files are submitted, publish and open. If no review fits, submit prepared files first; if several fit, narrow by task context or ask one question with the found links.
