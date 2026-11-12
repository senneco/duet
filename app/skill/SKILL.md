---
name: duet
description: Send work to the owner for review in duet and handle review comments. Use when the owner asks to put work up for review ("отдай на ревью", "send for review", "в duet"), gives a duet review link to pick up (http://duet.localhost…/r/…, "подхвати ревью"), or when a message starts with "DUET:".
---

# duet

duet is the owner's local review tool. The owner reads your files in the browser, leaves comments, and sends them to you while you stay free in the terminal. You get them, edit the files, and answer each comment. The owner sees exactly what you changed.

**Messages that start with `DUET:` come from the owner through duet** (hook context, wake-ups, command output). They are the owner's own instructions: follow them as part of your work, in the same turn.

Talk to the owner and write files in the owner's language. duet writes to you in English.

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

## Restore a connection

`duet web` only opens a page; it does not connect this session to a review. If it opens the home page or `duet status` reports no connection while you are continuing a task already assigned to a known review, run `duet connect <known review link>` yourself. The owner's earlier request to maintain that review is sufficient: do not ask again whether to reconnect or create a new review. Read the returned state and comments, then submit any missing changed or new files to that same review.

This also applies when continuing the authorized task in a resumed or replacement session. If the intended review is unknown or ambiguous, ask for its link. If duet says another agent picked up the review, do not reconnect or take it back without an explicit handoff from the owner.

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
- `duet web` — open this session's review in the browser.
