# OpenAI Codex and ChatGPT

**Codex (CLI, IDE extension, desktop app).** Codex reads Agent Skills from
`~/.agents/skills` (yours, every repository) and `.agents/skills` (one
repository):

```bash
mkdir -p ~/.agents/skills && cp -r llm/skills/fss ~/.agents/skills/
```

Restart Codex if the skill does not appear. `$fss` calls it by name.

**ChatGPT.** Zip the folder (`cd llm/skills && zip -r fss.zip fss`) and upload
it under Skills → Create → Upload from your computer. ChatGPT's own sandbox
cannot reach your machine, so there it can only read fss output files you
upload. To have ChatGPT actually run fss, use Codex.

Without skills support, paste the body of [`SKILL.md`](../skills/fss/SKILL.md)
(everything below the second `---`) into a project's or custom GPT's instructions.
