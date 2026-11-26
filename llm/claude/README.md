# Claude

**Claude Code.** Copy or link the skill folder into your skills directory:

```bash
# for you, in every project
mkdir -p ~/.claude/skills && cp -r llm/skills/fss ~/.claude/skills/
# or for one project only
mkdir -p .claude/skills && cp -r llm/skills/fss .claude/skills/
```

Start a new session. Claude picks the skill up when you ask about scraping a
studio, Stash imports and so on; `/fss` calls it by name.

**Claude desktop and claude.ai.** Zip the folder (`cd llm/skills && zip -r fss.zip fss`)
and upload it under Settings → Capabilities → Skills. Claude there can only run
fss if it has a shell on your machine, for example through Claude Code or a
desktop extension that runs commands. In the browser sandbox alone it can read
fss output files you upload, using the schema the skill points to, but it cannot
scrape.
