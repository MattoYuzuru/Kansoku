# Kansoku Codex Canary Task

This is a synthetic, harmless task used only to drive the Kansoku Codex
adapter's canary chain (`session.started` -> `prompt.submitted` ->
`component.invoked` -> `tool.called` -> `session.stopped`). It is never run
against a real user repository, and it is never run interactively.

## Task

1. Read the generated file `canary.txt` in this workspace.
2. Invoke the `kansoku-canary-skill` skill.
3. Call the `echo` tool on the local `kansoku-canary-echo-mcp` MCP server
   with the contents of `canary.txt`.
4. Stop.

## Execution constraints (must all hold before this task is ever run)

- Non-interactive only.
- Requires explicit user consent and a bounded budget
  (`max_turns=3`, `max_wall_clock_seconds=120`, `max_tool_calls=5`).
- Never uses a real user repository: this task and its generated workspace
  live entirely under a temporary directory created by a lifecycle separate
  from the canary run itself.
- The generated workspace is deleted through that same separately controlled
  temp lifecycle, never by the run being measured.
