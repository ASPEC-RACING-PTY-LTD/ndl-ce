# AI

Ask, Plan, Operate, and Automate are optional. Offline install has no
vendor and the platform still works. AI cannot Host.Exec.

## Ask

Read-only. Cites events and metrics. A profile without `events.read`
and `metrics.read` cannot query. Provider down still returns local
citations.

```text
nodalctl ai ask --prompt "Why did this workload restart?"
```

## Plan and Operate

Plans are reviewable lists of existing APIs. Approve requires
`X-Nodal-Confirm: approve-plan`. Ask profiles cannot operate. Missing
permissions stop the plan. Partial failure leaves audit. Operate must
call those existing APIs. Restart, Store install, and policy create
invoke the HTTP handlers. This is not Host.Exec.

## Automate

Storage-pressure intent binds to the Phase 40 policy engine. It is not
an LLM loop.

BYO providers: OpenAI, Anthropic, Gemini, Ollama, local,
openai-compatible, private. API keys stay in secrets.
