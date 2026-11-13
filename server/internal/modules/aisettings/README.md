# AI Settings

## Agent catalog

`GET /v1/aiAgents` returns ARTEX's built-in `goals`, `planner`, `mainagent`, `worker`, `auto`, and `pentest` Agents plus tenant custom Agents. The legacy `assistant` key resolves to `mainagent` for existing conversations. New conversations select an Agent key and store it on `ai_conversation`.

Custom Agents are administrator-managed rows in `ai_custom_agent`. They carry a key, display name, description, system prompt, enabled state, turn limit, and run-time limit. They use the active LLM profile and inherit the assistant platform-tool settings. Skill and MCP visibility is bound to the Agent key.

Tool bindings do not supply a task context. Independent conversations hide
graph-only tools such as `report_finding` and `list_findings`, including when
they are enabled in the catalog. They retain explicit-ID tools such as
`list_task_findings` and `add_task_hint` according to their normal bindings.
Task runners retain their real graph-backed handlers; global fallback metadata
must not replace them.

## Built-in Skills

`resources/skills` is copied from ARTEX and mounted in the Server image at `/usr/local/share/lunafox/skills`. `resources/skills/manifest.json` is the shipped catalog and default Agent-visibility map. Startup inserts each skill once by `builtin_key`; later startup preserves administrators' enabled and visibility changes. Built-in skills are initially imported once. Administrators can edit or delete them, and subsequent startups preserve those changes; custom skills remain user-managed.

The information-gathering coverage fix upgrades its shipped script group only when every existing script matches either the original or the already-updated version. A conflicting edit or deletion skips the complete script upgrade and logs the conflicting path. The skill document, visibility, enabled state, and all unrelated files are never changed by this compatibility update.

The information-gathering Agent can invoke Skill phases separately and decide between them. The phase adapter is added only when the installed pipeline still matches the compatible shipped version; otherwise the existing package is preserved. The default Agent prompt is upgraded only when it exactly matches the old shipped prompt, and its previous version remains in prompt history.

The resumable phase update likewise changes the adapter only when the installed phase scripts match the previous shipped version. A customized package remains untouched.

The information-gathering risk contract update accepts DNS and cloud-storage risk candidates. It updates only the unchanged shipped schema file; administrators' customized Skill documents and files are preserved.

The ARTEX default visibility is preserved: `api-recon` is visible to `auto`, `pentest`, and `worker`; `playwright-cli` and `scopesentry` begin with no Agent bindings.

## LLM defaults

The LLM page reuses ARTEX's cards, profile drawer and five-layer retry editor. Profiles retain existing UUIDs and encrypted API keys; per-profile proxies are encrypted too. Draft profiles may be saved without a key and remain unrunnable. Activation is explicit and an active profile cannot be deleted. Editing a profile preserves activation and an empty key retains its credential.

Profile options include shared RPS/RPM limits, context window, thinking type and effort, streaming mode, output cap and field, session header, priority, pool exclusion and retry overrides. The runtime keeps a provider and limiter per configuration, reuses ARTEX's failover/circuit-breaker implementation and persists breaker state. Agent binding remains above conversation/task selection and the global profile. A configured system proxy overrides the profile proxy when enabled.

The environment fallback delegates to ARTEX's provider defaults (Anthropic `claude-opus-4-8`, OpenAI `gpt-4o`, Responses `gpt-5`). No credentials are seeded.
