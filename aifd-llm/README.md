# aifd-llm

`aifd-llm` provides a provider-neutral LLM calling surface for backend services
and desktop Go bindings. It is a separately scoped package within the framework root Go module, not a runtime module
and not a feature plugin.

## Responsible For

- Chat-style request/response contracts.
- Streaming event contracts.
- Stable error kinds for auth, rate limit, provider, unsupported, and context failures.
- JSON output, usage, and tool call shapes.
- Provider adapters for OpenAI-compatible APIs, DeepSeek, Anthropic, and fake tests.

## Not Responsible For

- Runtime service/root lifecycle.
- Global queues, quota systems, prompt management, audit storage, or billing policy.
- Runtime AI agent UI protocols.
- Robot behavior trees or behavior3go compatibility.

## Usage

```go
client := deepseek.NewClient(deepseek.Options{APIKey: apiKey})
rsp, err := client.Complete(ctx, llm.Request{
    Model: model, // application-selected model; omit to use the adapter's DefaultModel
    Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}},
    ProviderOptions: map[string]any{"thinking": map[string]string{"type": "disabled"}},
})
```

Business code should depend on the `llm.Client` interface and keep provider
SDK or HTTP details inside adapters.

The caller supplies a bounded context and handles errors. For streaming, call
`Stream(ctx, req)`, receive events with `Recv()`, and always close the reader.
Tool calls are returned as data; the library does not execute business tools or run an agent loop.

OpenAI-compatible adapters (including DeepSeek) serialize `ProviderOptions` as
additional top-level JSON request fields in both `Complete` and `Stream`.
Extensions must be JSON serializable and cannot replace common fields such as
`model`, `messages`, `max_tokens`, `stream`, `response_format`, or `tools`, even
when the common field is omitted. Invalid extensions fail before HTTP with
`KindInvalidRequest`. Providers remain responsible for validating their own
extension names and values. Do not put credentials in request extensions.

Verify provider settings at the HTTP boundary; inspecting the neutral `Request`
alone does not prove that an adapter forwarded them. Tests cover forwarding,
streaming, reserved-field collisions, and invalid values. No protobuf, runtime,
or stored-data migration is required; callers that formerly supplied a reserved
common field in `ProviderOptions` must use the corresponding `Request` field.

`ProviderOptions` forwarding described above applies to the OpenAI-compatible adapters;
the current Anthropic adapter does not serialize that map. Check the owning adapter
before relying on a provider extension. Contracts live in [client.go](client.go) and
[stream.go](stream.go); defaults and mapping live in [adapters](adapters).

## Related documentation

- [Project home](../README.md)
- [Module boundaries (Chinese)](../docs/module-boundary.md)
- [Provider adapter coding rules (Chinese)](../aifd-rules/skills/project-coding-constraints/SKILL.md)
