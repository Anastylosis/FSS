# Grok

**Grok Build (CLI).** Copy the skill folder into `~/.grok/skills`:

```bash
mkdir -p ~/.grok/skills && cp -r llm/skills/fss ~/.grok/skills/
```

Start a new session; skills load at start-up. `/fss` calls it by name. Grok
Build also reads Claude's `.claude/skills` folders, so a project that already
has the skill there for Claude needs nothing more.

**Grok on the web or mobile.** Grok can save custom skills there too, but it has
no shell on your machine, so it cannot run fss. Paste the body of
[`SKILL.md`](../skills/fss/SKILL.md) as a skill if you want help reading fss
output you share with it.
