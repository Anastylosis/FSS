# fss for LLM agents

This directory lets an AI assistant run fss for you: check a site, scrape a
studio, read the results, push them into Stash.

It holds one skill, [`skills/fss/`](skills/fss/SKILL.md). It is written in the
[Agent Skills](https://agentskills.io) format, a `SKILL.md` file with a short
header, which Claude, OpenAI Codex and ChatGPT, Grok Build and most open-source
coding agents all read. The folders below only say where each one expects it.

| Assistant | How | Notes |
|---|---|---|
| Claude Code, Claude desktop, claude.ai | [claude/](claude/README.md) | Skill folder or zip upload |
| OpenAI Codex, ChatGPT | [chatgpt/](chatgpt/README.md) | Codex runs fss; ChatGPT on the web cannot |
| Grok Build | [grok/](grok/README.md) | Skill folder |
| OpenCode and other open-source agents, or a local model through Ollama, llama.cpp or vLLM | [open-models/](open-models/README.md) | Skill folder, or system prompt plus a tool definition |

Whichever you use, the assistant needs a shell on a machine where `fss` is
installed ([install.md](../docs/install.md)). The skill tells it to use
`--format json` ([json-output.md](../docs/json-output.md)) and to ask you before
anything that deletes or overwrites data: a `--full` or `--refresh` scrape,
`--force`, `stash import --apply`, `identify --apply`, `doctor --prune`.

A test (`TestLLMDocsNameRealCommands`) fails when a command or flag named in this
directory stops existing, so the skill cannot silently drift from the CLI.
