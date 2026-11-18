# Install or update duet

These steps are for an agent (Claude Code, Codex) whose owner gave it a link to duet and asked to install or update it. Do everything yourself; talk to the owner in their language. The same steps update an installed duet.

duet is a local tool: the owner reviews your work in the browser and sends you comments. It installs into the owner's home folder: the `duet` command into `~/.local/bin`, the duet skill and hooks into Claude Code and Codex — only those present on this machine. The owner's other settings stay as they are; the previous settings files are kept next to them as `*.duet-backup`.

## 1. Run the installer

```sh
curl -fsSL https://raw.githubusercontent.com/senneco/duet/main/install.sh | sh
```

- If `curl` gets 404, the repository is private. Use the GitHub CLI instead (it uses the owner's GitHub login):
  ```sh
  gh api repos/senneco/duet/contents/install.sh -H "Accept: application/vnd.github.raw" | sh
  ```
  If `gh` is missing or not logged in, ask the owner to log in (`gh auth login`), then run it again.
- If you work inside a clone of duet, run `./install.sh` there: with Go it builds duet from that clone.
- The installer needs network and writes outside your working folder: ask for the permissions it needs (in Codex, run it outside the sandbox).

The installer downloads a ready duet for this machine (macOS or Linux), so nothing else needs installing.

## 2. Check it works

```sh
duet status
```

It starts duet in the background and says that this session is not connected to a review: that means duet works. If `duet` is not found, use `~/.local/bin/duet status` and see step 3.

## 3. Tell the owner what is left

The installer prints this at the end under "Tell the owner". Usually:

- **Start a new agent session.** duet works in sessions started after the install; this one has no duet hooks yet.
- **Codex** asks once to trust the duet hooks: allow them.
- If `~/.local/bin` is not in `PATH`, offer to add it to the owner's shell profile (for zsh: `export PATH="$HOME/.local/bin:$PATH"` in `~/.zshrc`) and do it if they agree.
- To use duet: ask the agent "send it for review" ("отдай на ревью"). The agent writes a review link in the chat.

## Плагины репозитория

Git поддерживается без настройки. Другие системы подключаются отдельными локальными программами после установки duet: [регистрация, проверка и диагностика](docs/vcs-plugins.md). Без соответствующего плагина распознавание сторонней системы не гарантируется.
