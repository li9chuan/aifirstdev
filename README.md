# aiFirstDev

English | [简体中文](./README.zh-CN.md)

**v0.1.0 is the first development snapshot. APIs may change incompatibly.**
The canonical repository is [li9chuan/aifirstdev](https://github.com/li9chuan/aifirstdev).
The desktop example currently targets Windows amd64 and Wails 3 beta.28.

aiFirstDev is a lightweight backend framework for AI-first development. It is
organized as multiple Go modules in one workspace. The current runtime first
covers game backend scenarios, while the framework language uses service/root
runtime concepts. Business applications name their own backend services by
domain, making the framework extensible to Wails tools, desktop applications,
and other business backends.

The first-stage split keeps infrastructure modules independent while business
projects continue to write feature code in the normal way:

- `aifd-contracts`: minimal shared contracts.
- `aifd-auth`: signed tickets shared by the account center and proxy.
- `aifd-account`: login identity, global account IDs, provider binding, and
  login payload/token issuance. Business user, owner, and realm mappings belong
  to the application.
- `aifd-client`: Go client connections, login, message routing, and requests.
- `aifd-config`: typed configuration bundle lifecycle, validation, manifest diff,
  and Luban bundle builder adapters.
- `aifd-llm`: provider-neutral LLM request, response, stream, error, and adapter
  contracts.
- `aifd-observe`: observable events, async sinks, and local statistics adapters.
- `aifd-proxy`: client gateway contracts, sessions, delivery, and adapters.
- `aifd-robot`: integration test robots, run groups, behaviors, and statistics.
- `aifd-rpc`: RPC contracts, routing, dispatchers, and adapters.
- `aifd-rdb`: persistence saver primitives and storage adapters.
- `aifd-runtime`: service/root runtime, root store, module lifecycle, and
  configuration/lifecycle helpers.
- `aifd-schema`: proto/message descriptions, compatibility checks, and RPC
  binding.
- `aifd-tools`: CLI, tool installation, and a reserved update command.
- `aifd-rules`: AI rules, skills, templates, and checklists.
- `aifd-plugins/mail`: optional mail storage, mailbox access, and runtime modules,
  explicitly composed by the application.
- `aifd-examples`: business-shape examples. `minigame` demonstrates the full
  game backend framework flow, and `desktool` demonstrates a desktop tool.

Dependencies must remain one-way:

```text
aifd-contracts        aifd-llm
  ^                     ^
  +-- aifd-auth         |
  +-- aifd-account      |
  +-- aifd-config       |
  +-- aifd-observe      |
  +-- aifd-proxy        |
  +-- aifd-robot        |
  +-- aifd-rpc          |
  +-- aifd-rdb          |
  +-- aifd-schema       |
  ^                     |
aifd-runtime           |
  ^                     |
business projects -----+
```

`aifd-config`, `aifd-llm`, `aifd-observe`, `aifd-proxy`, `aifd-robot`,
`aifd-rpc`, `aifd-rdb`, and `aifd-schema` do not understand player, world,
feature, DBPlayer, concrete `.proto` files, real Luban tables, or business
prompt policy. `aifd-runtime` provides `service.Service` and `root.Runtime`;
game, player, world, and similar terms belong only to business examples, not to
the framework public API.

The framework is still in a new-design stage. Framework APIs are intentionally
allowed to converge through breaking changes toward the smallest business-facing
surface. Experimental legacy interfaces do not keep compatibility wrappers.
Complexity should move down into the framework layer, while business projects
only touch the necessary business parameters and domain objects.

Module taxonomy and future `aifd-core` admission rules are described in
[docs/module-boundary.md](docs/module-boundary.md). Scope runtime organization
principles are described in [docs/scope-runtime.md](docs/scope-runtime.md).

## Quick start

AI-first describes the development workflow: repository rules, skills, templates,
and reproducible checks help AI agents work within module boundaries. Applications
do not require an LLM at runtime; `aifd-llm` is optional.

Install Go 1.26 or later and Git, then start the in-memory backend example:

```powershell
git clone https://github.com/li9chuan/aifirstdev.git
cd aifirstdev/aifd-examples/minigame
go run ./cmd/minigame
```

In a second terminal, from the same `aifd-examples/minigame` directory:

```powershell
go run ./cmd/client -uid 1001 -counter-delta 1
```

On a fresh server the client prints `auth ret=0` and counter `value=1`.
This default example uses local RPC and memory storage; no Redis or NATS service
is required. Stop the server with Ctrl+C. See [minigame](aifd-examples/minigame/README.md)
for split services and [desktool](aifd-examples/desktool/README.md) for the desktop example.
Desktop builds additionally need Node.js/npm and the pinned Wails CLI; Windows
installer builds need NSIS.

## Use from another Go project

From an initialized Go module outside this workspace, add only the modules needed:

```powershell
go get github.com/li9chuan/aifirstdev/aifd-runtime@v0.1.0
go get github.com/li9chuan/aifirstdev/aifd-client@v0.1.0
go install github.com/li9chuan/aifirstdev/aifd-tools/cmd/aifd@v0.1.0
```

There is no Go module at the repository root. The 17 modules retain independent
paths under `github.com/li9chuan/aifirstdev/`; the repository uses `go.work` for
local development. Nested module tags include their directory, such as
`aifd-runtime/v0.1.0` and `aifd-plugins/mail/v0.1.0`. The root tag `v0.1.0` identifies
the complete source snapshot. This first release does not promise stable APIs or
parallel maintenance branches. See [version and update policy](docs/update-policy.md).

`aifd update` is a reserved placeholder: it prints guidance but does not check,
download, or apply updates. Third-party tool versions remain pinned in `aifd.lock`.

## Build and verify

Use Go 1.26 or later, Node.js 22.12 or later with npm, and PowerShell 5.1 or 7.
Put `go`, `node`, and `npm` on PATH, then run from the repository root:

```powershell
.\scripts\verify.ps1
```

If Windows PowerShell 5.1 blocks local scripts, run
`powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\verify.ps1`.
This sets the execution policy for that process only.

The script installs the locked frontend dependencies with `npm ci`, tests and
builds both the desktool frontend and proxy Web client, then runs
`go test -mod=readonly -count=1 -timeout=2m ./...` in every module listed in
`go.work`. Root-level `go test ./...` does not traverse this multi-module workspace.
The first run needs network access to download dependencies.

Desktool embeds `frontend/dist` in Go; build its frontend before running its Go
tests. Frontend output and installer downloads are generated locally and are not
versioned. Protobuf sources, generated bindings, and dependency lockfiles remain
in the repository.

The two external NATS integration tests are skipped when `AIFD_NATS_URL` is unset.
Set it to a reachable test NATS server to include them; the verification script
does not start an external server. Native desktop execution and Windows installer
builds are separate checks; see [desktool](aifd-examples/desktool/README.md).

## License

Copyright 2026 aiFirstDev contributors. Licensed under [Apache-2.0](LICENSE).
Third-party components retain their own licenses; the desktop packaging assets
and templates have [third-party notices](aifd-examples/desktool/THIRD_PARTY_NOTICES).
