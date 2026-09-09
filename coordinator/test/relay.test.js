import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { createHash, randomUUID } from "node:crypto";
import { Miniflare } from "miniflare";
let mf;
const thread = randomUUID();
const tokens = {
  linux: "test-linux-token",
  windows: "test-windows-token",
  other: "test-other-token",
};
async function call(host, path, body, token = tokens[host]) {
  const r = await mf.dispatchFetch("http://relay.test" + path, {
    method: body ? "POST" : "GET",
    headers: { "X-Swarm-Host": host, Authorization: `Bearer ${token}` },
    body: body ? JSON.stringify(body) : undefined,
  });
  return { status: r.status, data: await r.json() };
}
before(async () => {
  mf = new Miniflare({
    modules: true,
    scriptPath: new URL("../src/index.js", import.meta.url).pathname,
    compatibilityDate: "2026-07-30",
    d1Databases: ["DB"],
    bindings: {
      HOST_KEYS_JSON: JSON.stringify(
        Object.fromEntries(
          Object.entries(tokens).map(([h, t]) => [
            h,
            createHash("sha256").update(t).digest("hex"),
          ]),
        ),
      ),
    },
  });
  const db = await mf.getD1Database("DB");
  const sql = await readFile(
    new URL("../migrations/0001_relay.sql", import.meta.url),
    "utf8",
  );
  await db.batch(
    sql
      .split(";")
      .map((s) => s.trim())
      .filter(Boolean)
      .map((s) => db.prepare(s)),
  );
});
after(async () => mf?.dispose());
test("host authentication is required", async () =>
  assert.equal(
    (await call("windows", "/v1/tasks", null, "wrong")).status,
    401,
  ));
test("end-to-end mailbox, retries, ownership and explicit completion", async () => {
  assert.equal(
    (
      await call("windows", "/v1/tasks", {
        thread_id: thread,
        title: "test",
        enabled: true,
      })
    ).status,
    200,
  );
  const b = {
    request_id: randomUUID(),
    target_host: "windows",
    thread_id: thread,
    prompt: "bounded handoff",
  };
  const sent = await call("linux", "/v1/messages", b);
  assert.equal(sent.status, 200);
  assert.equal(sent.data.state, "queued");
  assert.equal((await call("linux", "/v1/messages", b)).data.id, sent.data.id);
  assert.equal(
    (await call("linux", "/v1/messages", { ...b, prompt: "different" })).status,
    409,
  );
  assert.equal(
    (await call("other", "/v1/messages/" + encodeURIComponent(sent.data.id)))
      .status,
    404,
  );
  const attempt = { request_id: randomUUID() };
  const claims = await Promise.all([
    call("windows", "/v1/claim", attempt),
    call("windows", "/v1/claim", attempt),
  ]);
  assert.equal(claims[0].data.message.id, sent.data.id);
  assert.equal(claims[1].data.message.id, sent.data.id);
  assert.equal(
    (await call("windows", "/v1/claim", { request_id: randomUUID() })).data
      .message,
    null,
  );
  const receipt = {
    request_id: randomUUID(),
    message_id: sent.data.id,
    state: "submitted",
    submission_id: randomUUID(),
  };
  assert.equal((await call("linux", "/v1/receipts", receipt)).status, 409);
  assert.equal((await call("windows", "/v1/receipts", receipt)).status, 200);
  assert.equal(
    (
      await call("windows", "/v1/receipts", {
        ...receipt,
        submission_id: randomUUID(),
      })
    ).status,
    409,
  );
  assert.equal(
    (
      await call("windows", "/v1/receipts", {
        request_id: randomUUID(),
        message_id: sent.data.id,
        state: "completed",
        evidence: "not acknowledged yet",
      })
    ).status,
    409,
  );
  assert.equal(
    (
      await call("windows", "/v1/receipts", {
        request_id: randomUUID(),
        message_id: sent.data.id,
        state: "acknowledged",
        evidence: "destination turn observed",
      })
    ).status,
    200,
  );
  assert.equal((await call("windows", "/v1/receipts", receipt)).status, 200);
  assert.equal(
    (await call("linux", "/v1/messages/" + encodeURIComponent(sent.data.id)))
      .data.state,
    "acknowledged",
  );
  assert.equal(
    (
      await call("windows", "/v1/receipts", {
        request_id: randomUUID(),
        message_id: sent.data.id,
        state: "completed",
        evidence: "result verified",
      })
    ).status,
    200,
  );
});
test("concurrent distinct attempts cannot both acquire a message; uncertainty never resends", async () => {
  const b = {
    request_id: randomUUID(),
    target_host: "windows",
    thread_id: thread,
    prompt: "one delivery",
  };
  const sent = await call("linux", "/v1/messages", b);
  const claims = await Promise.all(
    Array.from({ length: 5 }, () =>
      call("windows", "/v1/claim", { request_id: randomUUID() }),
    ),
  );
  assert.equal(claims.filter((c) => c.data.message).length, 1);
  assert.equal(
    (
      await call("windows", "/v1/receipts", {
        request_id: randomUUID(),
        message_id: sent.data.id,
        state: "uncertain",
        evidence: "crash during subprocess",
      })
    ).status,
    200,
  );
  assert.equal(
    (await call("windows", "/v1/claim", { request_id: randomUUID() })).data
      .message,
    null,
  );
});
test("unenrolled and disabled tasks refuse new work; empty claim replays stay empty", async () => {
  const attempt = { request_id: randomUUID() };
  assert.equal(
    (await call("windows", "/v1/claim", attempt)).data.message,
    null,
  );
  assert.equal(
    (
      await call("linux", "/v1/messages", {
        request_id: randomUUID(),
        target_host: "windows",
        thread_id: randomUUID(),
        prompt: "no",
      })
    ).status,
    409,
  );
  await call("windows", "/v1/tasks", {
    thread_id: thread,
    title: "test",
    enabled: false,
  });
  assert.equal(
    (
      await call("linux", "/v1/messages", {
        request_id: randomUUID(),
        target_host: "windows",
        thread_id: thread,
        prompt: "no",
      })
    ).status,
    409,
  );
  assert.equal(
    (await call("windows", "/v1/claim", attempt)).data.message,
    null,
  );
});
test("shared claims warn about overlap, permit both hosts and enforce release ownership", async () => {
  const b = {
    request_id: randomUUID(),
    resource: "repo:github.com/example/repo:path:src",
    note: "bounded edit",
  };
  const one = await call("linux", "/v1/claims", b);
  assert.equal(one.status, 200);
  assert.equal(one.data.other_active_claims, 0);
  assert.equal(
    (await call("linux", "/v1/claims", b)).data.claim.id,
    one.data.claim.id,
  );
  assert.equal(
    (await call("linux", "/v1/claims", { ...b, note: "changed" })).status,
    409,
  );
  const two = await call("windows", "/v1/claims", {
    ...b,
    request_id: randomUUID(),
  });
  assert.equal(two.status, 200);
  assert.equal(two.data.other_active_claims, 1);
  assert.equal(
    (await call("windows", "/v1/claims/release", { id: one.data.claim.id }))
      .status,
    404,
  );
  assert.equal(
    (await call("linux", "/v1/claims/release", { id: one.data.claim.id }))
      .status,
    200,
  );
  assert.equal(
    (await call("linux", "/v1/claims/release", { id: one.data.claim.id }))
      .status,
    200,
  );
  const listed = await call(
    "linux",
    "/v1/claims?resource=" + encodeURIComponent(b.resource),
  );
  assert.equal(listed.data.claims.length, 1);
});
test("idle readiness checks do not grow the database", async () => {
  const db = await mf.getD1Database("DB");
  const before = await db
    .prepare("SELECT COUNT(*) AS count FROM attempts")
    .first();
  for (let i = 0; i < 5; i++)
    assert.equal((await call("windows", "/v1/ready")).data.ready, false);
  const after = await db
    .prepare("SELECT COUNT(*) AS count FROM attempts")
    .first();
  assert.deepEqual(after, before);
});
test("bounds request size and requires observed evidence", async () => {
  const oversized = await call("linux", "/v1/messages", {
    request_id: randomUUID(),
    target_host: "windows",
    thread_id: thread,
    prompt: "x".repeat(30000),
  });
  assert.equal(oversized.status, 413);
  assert.equal(
    (
      await call("windows", "/v1/receipts", {
        request_id: randomUUID(),
        message_id: "linux:" + randomUUID(),
        state: "acknowledged",
      })
    ).status,
    400,
  );
});
test("task inventory pagination preserves more than 100 tasks", async () => {
  const db = await mf.getD1Database("DB");
  const ids = Array.from({ length: 105 }, () => randomUUID()).sort();
  await db.batch(
    ids.map((id) =>
      db
        .prepare(
          "INSERT INTO tasks(host_id,thread_id,title,enabled,updated_at) VALUES(?,?,?,?,?)",
        )
        .bind("windows", id, "fixture", 1, new Date().toISOString()),
    ),
  );
  const first = await call("linux", "/v1/tasks?host=windows");
  assert.equal(first.data.tasks.length, 100);
  assert.ok(first.data.next_cursor);
  const second = await call(
    "linux",
    "/v1/tasks?host=windows&after=" + first.data.next_cursor,
  );
  assert.equal(second.data.next_cursor, "");
  assert.equal(
    new Set([...first.data.tasks, ...second.data.tasks].map((t) => t.thread_id))
      .size,
    106,
  );
});
