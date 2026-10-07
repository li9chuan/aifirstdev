import assert from "node:assert/strict";
import test from "node:test";
import { selectBackend, withStartupTimeout } from "../src/backend-mode.ts";

test("a missing native transport cannot leave startup pending forever", async () => {
  await assert.rejects(withStartupTimeout(new Promise(() => {}), 10), /startup timed out/);
  assert.equal(await withStartupTimeout(Promise.resolve("native data"), 100), "native data");
});

test("explicit preview does not initialise the native runtime", async () => {
  const preview = { source: "preview" };
  assert.equal(await selectBackend("preview", () => { throw new Error("native must not load"); }, preview), preview);
});

for (const mode of ["development", "production", ""]) {
  test(`${mode || "unset"} mode uses the native backend`, async () => {
    const native = { source: "native" };
    assert.equal(await selectBackend(mode, async () => native, { source: "preview" }), native);
  });

  test(`${mode || "unset"} mode surfaces a failed native binding`, async () => {
    const failure = new Error("native binding unavailable");
    await assert.rejects(selectBackend(mode, async () => { throw failure; }, { source: "preview" }), failure);
  });
}
