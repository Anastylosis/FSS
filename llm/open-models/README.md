# Open-source agents and local models

**Agents that read skills.** OpenCode looks in `.opencode/skills/` and also
reads Claude's `.claude/skills/` and the shared `.agents/skills/`; Zed and pi
read `SKILL.md` folders too. Copy [`skills/fss`](../skills/fss/SKILL.md)
into whichever folder your agent uses, as for [Claude](../claude/README.md).

**A model with no agent around it** (Ollama, llama.cpp `llama-server`, vLLM,
LM Studio) needs two things from you:

1. A system prompt: the body of [`SKILL.md`](../skills/fss/SKILL.md), everything
   below the second `---`.
2. A tool to call: [`tools.json`](tools.json) defines one function, `run_fss`,
   in the OpenAI function-calling format all four servers accept. Your program
   runs `fss` with the arguments the model asks for (as an argument list,
   never through a shell), adds `--format json` if missing, and sends back
   stdout, stderr and the exit code.

Before running a call, check the arguments yourself: refuse `--apply`,
`--force`, `--full`, `--refresh`, `--prune` and `--update` unless you have
confirmed them with the person. Small models follow the skill's "ask first"
rules less reliably than large ones.

Use a model trained for tool calling.
