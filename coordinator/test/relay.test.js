import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { readFile, readdir } from "node:fs/promises";
import { createHash, randomUUID } from "node:crypto";
import { Miniflare } from "miniflare";
import { compactRelay } from "../src/index.js";
let mf;
const thread = randomUUID();
const tokens = {
  linux: "test-linux-token",
  windows: "test-windows-token",
  other: "test-other-token",
};
async function call(host, path, body, token = tokens[host]) {
  return relayCall(mf, host, path, body, token);
}
async function relayCall(relay, host, path, body, token = tokens[host]) {
  const r = await relay.dispatchFetch("http://relay.test" + path, {
    method: body ? "POST" : "GET",
    headers: { "X-Swarm-Host": host, Authorization: `Bearer ${token}` },
    body: body ? JSON.stringify(body) : undefined,
  });
  return { status: r.status, data: await r.json() };
}
async function startRelay(quotaLimits) {
  const relay = new Miniflare({
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
      RELAY_QUOTA_LIMITS_JSON:
        typeof quotaLimits === "string"
          ? quotaLimits
          : JSON.stringify(quotaLimits ?? {}),
    },
  });
  const db = await relay.getD1Database("DB");
  const migrations = new URL("../migrations/", import.meta.url);
  for (const name of (await readdir(migrations)).sort()) {
    if (!name.endsWith(".sql")) continue;
    const sql = await readFile(new URL(name, migrations), "utf8");
    await db.batch(
      sql
        .split(";")
        .map((s) => s.trim())
        .filter(Boolean)
        .map((s) => db.prepare(s)),
    );
  }
  return relay;
}
before(async () => {
  mf = await startRelay();
});
after(async () => mf?.dispose());
test("host authentication is required", async () =>
  assert.equal(
    (await call("windows", "/v1/tasks", null, "wrong")).status,
    401,
  ));
test("invalid quota configuration cannot disable the checked-in limits", async () => {
  const relay = await startRelay("null");
  try {
    assert.equal(
      (
        await relayCall(relay, "windows", "/v1/tasks", {
          thread_id: randomUUID(),
          title: "default limits",
          enabled: true,
        })
      ).status,
      200,
    );
  } finally {
    await relay.dispose();
  }
});
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
test("aggregate quotas reject new rows but preserve idempotent replays", async () => {
  const relay = await startRelay({
    tasksPerHost: 1,
    claimsPerHost: 2,
    activeClaimsPerHost: 1,
    attemptsPerHost: 1,
    messagesPerSender: 2,
    activeMessagesPerSender: 1,
    activeMessagesPerTarget: 1,
  });
  try {
    const task = {
      thread_id: randomUUID(),
      title: "quota target",
      enabled: true,
    };
    assert.equal(
      (await relayCall(relay, "windows", "/v1/tasks", task)).status,
      200,
    );
    assert.equal(
      (
        await relayCall(relay, "windows", "/v1/tasks", {
          thread_id: randomUUID(),
          title: "too many",
          enabled: true,
        })
      ).status,
      429,
    );
    assert.equal(
      (
        await relayCall(relay, "windows", "/v1/tasks", {
          ...task,
          title: "safe update at capacity",
        })
      ).status,
      200,
    );

    const claim = {
      request_id: randomUUID(),
      resource: "repo:quota",
      note: "first",
    };
    assert.equal(
      (await relayCall(relay, "linux", "/v1/claims", claim)).status,
      200,
    );
    assert.equal(
      (await relayCall(relay, "linux", "/v1/claims", claim)).status,
      200,
    );
    assert.equal(
      (
        await relayCall(relay, "linux", "/v1/claims", {
          ...claim,
          request_id: randomUUID(),
        })
      ).status,
      429,
    );

    const message = {
      request_id: randomUUID(),
      target_host: "windows",
      thread_id: task.thread_id,
      prompt: "first",
    };
    assert.equal(
      (await relayCall(relay, "linux", "/v1/messages", message)).status,
      200,
    );
    assert.equal(
      (await relayCall(relay, "linux", "/v1/messages", message)).status,
      200,
    );
    assert.equal(
      (
        await relayCall(relay, "linux", "/v1/messages", {
          ...message,
          request_id: randomUUID(),
        })
      ).status,
      429,
    );

    const attempt = { request_id: randomUUID() };
    assert.equal(
      (await relayCall(relay, "windows", "/v1/claim", attempt)).status,
      200,
    );
    assert.equal(
      (await relayCall(relay, "windows", "/v1/claim", attempt)).status,
      200,
    );
    assert.equal(
      (
        await relayCall(relay, "windows", "/v1/claim", {
          request_id: randomUUID(),
        })
      ).status,
      429,
    );
  } finally {
    await relay.dispose();
  }
});
test("concurrent senders cannot race past a target queue quota", async () => {
  const relay = await startRelay({ activeMessagesPerTarget: 1 });
  try {
    const targetThread = randomUUID();
    assert.equal(
      (
        await relayCall(relay, "windows", "/v1/tasks", {
          thread_id: targetThread,
          title: "bounded target",
          enabled: true,
        })
      ).status,
      200,
    );
    const sends = await Promise.all(
      ["linux", "other"].map((host) =>
        relayCall(relay, host, "/v1/messages", {
          request_id: randomUUID(),
          target_host: "windows",
          thread_id: targetThread,
          prompt: `from ${host}`,
        }),
      ),
    );
    assert.deepEqual(sends.map(({ status }) => status).sort(), [200, 429]);
    const db = await relay.getD1Database("DB");
    assert.equal(
      (
        await db
          .prepare(
            "SELECT COUNT(*) AS count FROM messages WHERE target_host='windows' AND state!='completed'",
          )
          .first()
      ).count,
      1,
    );
  } finally {
    await relay.dispose();
  }
});
test("compaction deletes expired terminal history but preserves replay-horizon rows", async () => {
  const relay = await startRelay();
  try {
    const db = await relay.getD1Database("DB");
    const old = "2020-01-01T00:00:00.000Z";
    const recent = "2026-10-01T00:00:00.000Z";
    const oldMessage = `linux:${randomUUID()}`;
    const recentMessage = `linux:${randomUUID()}`;
    await db.batch([
      db
        .prepare("INSERT INTO hosts(id,seen_at) VALUES(?,?)")
        .bind("linux", recent),
      db
        .prepare("INSERT INTO hosts(id,seen_at) VALUES(?,?)")
        .bind("windows", recent),
      db
        .prepare(
          "INSERT INTO tasks(host_id,thread_id,title,enabled,updated_at) VALUES(?,?,?,?,?)",
        )
        .bind("windows", thread, "fixture", 1, recent),
      ...[oldMessage, recentMessage].map((id, index) =>
        db
          .prepare(
            "INSERT INTO messages(id,sender_host,request_id,target_host,thread_id,prompt,state,created_at,updated_at) VALUES(?,?,?,?,?,?,'completed',?,?)",
          )
          .bind(
            id,
            "linux",
            id.slice("linux:".length),
            "windows",
            thread,
            "fixture",
            index === 0 ? old : recent,
            index === 0 ? old : recent,
          ),
      ),
      db
        .prepare(
          "INSERT INTO attempts(host_id,request_id,message_id,created_at) VALUES(?,?,?,?)",
        )
        .bind("windows", randomUUID(), oldMessage, old),
      db
        .prepare(
          "INSERT INTO attempts(host_id,request_id,message_id,created_at) VALUES(?,?,NULL,?)",
        )
        .bind("windows", randomUUID(), old),
      db
        .prepare(
          "INSERT INTO receipts(host_id,request_id,message_id,state,submission_id,evidence,created_at) VALUES(?,?,?,'completed','','done',?)",
        )
        .bind("windows", randomUUID(), oldMessage, old),
      db
        .prepare(
          "INSERT INTO claims(id,host_id,resource,note,created_at,released_at) VALUES(?,?,?,?,?,?)",
        )
        .bind(`linux:${randomUUID()}`, "linux", "repo:old", "old", old, old),
      db
        .prepare(
          "INSERT INTO claims(id,host_id,resource,note,created_at,released_at) VALUES(?,?,?,?,?,?)",
        )
        .bind(
          `linux:${randomUUID()}`,
          "linux",
          "repo:recent",
          "recent",
          recent,
          recent,
        ),
    ]);
    await compactRelay(db, "2026-09-01T00:00:00.000Z");
    assert.equal(
      (await db.prepare("SELECT COUNT(*) AS count FROM messages").first())
        .count,
      1,
    );
    assert.equal(
      (await db.prepare("SELECT COUNT(*) AS count FROM attempts").first())
        .count,
      0,
    );
    assert.equal(
      (await db.prepare("SELECT COUNT(*) AS count FROM receipts").first())
        .count,
      0,
    );
    assert.equal(
      (await db.prepare("SELECT COUNT(*) AS count FROM claims").first()).count,
      1,
    );
  } finally {
    await relay.dispose();
  }
});
