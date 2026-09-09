const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const hostID = /^[a-z0-9][a-z0-9-]{0,62}$/;
const json = (body, status = 200) =>
  Response.json(body, { status, headers: { "Cache-Control": "no-store" } });
class HTTPError extends Error {
  constructor(status, message) {
    super(message);
    this.status = status;
  }
}
function requireValue(ok, message, status = 400) {
  if (!ok) throw new HTTPError(status, message);
}
function text(value, max) {
  return (
    typeof value === "string" &&
    value.length > 0 &&
    new TextEncoder().encode(value).length <= max
  );
}
async function body(request) {
  // Bound chunked requests too, without buffering arbitrary input.
  const reader = request.body?.getReader();
  requireValue(reader, "JSON body required");
  let size = 0;
  const chunks = [];
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.length;
      requireValue(size <= 24576, "request too large", 413);
      chunks.push(value);
    }
  } finally {
    await reader.cancel();
  }
  const bytes = new Uint8Array(size);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.length;
  }
  let result;
  try {
    result = JSON.parse(new TextDecoder().decode(bytes));
  } catch {
    throw new HTTPError(400, "invalid JSON");
  }
  requireValue(
    result && typeof result === "object" && !Array.isArray(result),
    "JSON object required",
  );
  return result;
}
async function authenticate(request, env) {
  const host = request.headers.get("X-Swarm-Host");
  const authorization = request.headers.get("Authorization") ?? "";
  requireValue(
    hostID.test(host ?? "") && authorization.startsWith("Bearer "),
    "unauthorized",
    401,
  );
  const keys = JSON.parse(env.HOST_KEYS_JSON || "{}");
  const expected = keys[host];
  requireValue(
    typeof expected === "string" && /^[a-f0-9]{64}$/.test(expected),
    "unauthorized",
    401,
  );
  const digest = await crypto.subtle.digest(
    "SHA-256",
    new TextEncoder().encode(authorization.slice(7)),
  );
  const actual = [...new Uint8Array(digest)]
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
  let mismatch = 0;
  for (let i = 0; i < 64; i++)
    mismatch |= actual.charCodeAt(i) ^ expected.charCodeAt(i);
  requireValue(mismatch === 0, "unauthorized", 401);
  return host;
}
function messageID(host, request) {
  return `${host}:${request}`;
}
async function handle(request, env) {
  const host = await authenticate(request, env);
  const url = new URL(request.url),
    path = url.pathname;
  const db = env.DB,
    now = new Date().toISOString();
  const q = (sql, ...args) => db.prepare(sql).bind(...args);
  if (request.method === "POST" && path === "/v1/tasks") {
    const b = await body(request);
    requireValue(
      uuid.test(b.thread_id) &&
        text(b.title, 200) &&
        typeof b.enabled === "boolean",
      "invalid task",
    );
    await db.batch([
      q(
        "INSERT INTO hosts(id,seen_at) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET seen_at=excluded.seen_at",
        host,
        now,
      ),
      q(
        "INSERT INTO tasks(host_id,thread_id,title,enabled,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(host_id,thread_id) DO UPDATE SET title=excluded.title,enabled=excluded.enabled,updated_at=excluded.updated_at",
        host,
        b.thread_id,
        b.title,
        +b.enabled,
        now,
      ),
    ]);
    return json({ host_id: host, ...b });
  }
  if (request.method === "GET" && path === "/v1/tasks") {
    const target = url.searchParams.get("host") || host;
    requireValue(hostID.test(target), "invalid host");
    const cursor = url.searchParams.get("after") || "";
    requireValue(cursor === "" || uuid.test(cursor), "invalid cursor");
    const { results } = await q(
      "SELECT host_id,thread_id,title,enabled,updated_at FROM tasks WHERE host_id=? AND thread_id>? ORDER BY thread_id LIMIT 101",
      target,
      cursor,
    ).all();
    return json({
      tasks: results.slice(0, 100),
      next_cursor: results.length > 100 ? results[99].thread_id : "",
    });
  }
  if (request.method === "POST" && path === "/v1/claims") {
    const b = await body(request);
    requireValue(
      uuid.test(b.request_id) && text(b.resource, 512) && text(b.note, 1024),
      "invalid claim",
    );
    const id = messageID(host, b.request_id);
    await db.batch([
      q(
        "INSERT INTO hosts(id,seen_at) VALUES(?,?) ON CONFLICT(id) DO NOTHING",
        host,
        now,
      ),
      q(
        "INSERT INTO claims(id,host_id,resource,note,created_at) VALUES(?,?,?,?,?) ON CONFLICT(id) DO NOTHING",
        id,
        host,
        b.resource,
        b.note,
        now,
      ),
    ]);
    const claim = await q("SELECT * FROM claims WHERE id=?", id).first();
    requireValue(
      claim.resource === b.resource && claim.note === b.note,
      "claim replay mismatch",
      409,
    );
    // Overlap is advisory: never reject a claim because another host holds one.
    const overlap = await q(
      "SELECT COUNT(*) AS count FROM claims WHERE resource=? AND released_at IS NULL AND id!=?",
      b.resource,
      id,
    ).first();
    return json({ claim, other_active_claims: overlap.count });
  }
  if (request.method === "GET" && path === "/v1/claims") {
    const resource = url.searchParams.get("resource"),
      cursor = url.searchParams.get("after") || "";
    requireValue(
      text(resource, 512) && cursor.length <= 110,
      "invalid resource/cursor",
    );
    const { results } = await q(
      "SELECT * FROM claims WHERE resource=? AND released_at IS NULL AND id>? ORDER BY id LIMIT 101",
      resource,
      cursor,
    ).all();
    return json({
      claims: results.slice(0, 100),
      next_cursor: results.length > 100 ? results[99].id : "",
    });
  }
  if (request.method === "POST" && path === "/v1/claims/release") {
    const b = await body(request);
    requireValue(text(b.id, 110), "invalid claim ID");
    // The claim ID is the idempotency key for this terminal transition.
    const claim = await q(
      "UPDATE claims SET released_at=COALESCE(released_at,?) WHERE id=? AND host_id=? RETURNING *",
      now,
      b.id,
      host,
    ).first();
    requireValue(claim, "claim not found for this host", 404);
    return json({ claim });
  }
  if (request.method === "POST" && path === "/v1/messages") {
    const b = await body(request);
    requireValue(
      uuid.test(b.request_id) &&
        hostID.test(b.target_host) &&
        uuid.test(b.thread_id) &&
        text(b.prompt, 16384),
      "invalid message",
    );
    const id = messageID(host, b.request_id);
    // A replay must preserve the original content even if enrollment later changes.
    let m = await q("SELECT * FROM messages WHERE id=?", id).first();
    if (!m) {
      await db.batch([
        q(
          "INSERT INTO hosts(id,seen_at) VALUES(?,?) ON CONFLICT(id) DO NOTHING",
          host,
          now,
        ),
        q(
          "INSERT INTO messages(id,sender_host,request_id,target_host,thread_id,prompt,state,created_at,updated_at) SELECT ?,?,?,?,?,?,'queued',?,? WHERE EXISTS(SELECT 1 FROM tasks WHERE host_id=? AND thread_id=? AND enabled=1) ON CONFLICT(id) DO NOTHING",
          id,
          host,
          b.request_id,
          b.target_host,
          b.thread_id,
          b.prompt,
          now,
          now,
          b.target_host,
          b.thread_id,
        ),
      ]);
      m = await q("SELECT * FROM messages WHERE id=?", id).first();
    }
    requireValue(m, "target task is not enrolled", 409);
    requireValue(
      m.target_host === b.target_host &&
        m.thread_id === b.thread_id &&
        m.prompt === b.prompt,
      "request ID content mismatch",
      409,
    );
    return json(m);
  }
  if (request.method === "GET" && path === "/v1/ready") {
    const m = await q(
      "SELECT m.id FROM messages m JOIN tasks t ON t.host_id=m.target_host AND t.thread_id=m.thread_id WHERE m.target_host=? AND m.state='queued' AND t.enabled=1 LIMIT 1",
      host,
    ).first();
    return json({ ready: !!m });
  }
  if (request.method === "GET" && path === "/v1/inbox") {
    const cursor = url.searchParams.get("after") || "";
    requireValue(cursor.length <= 110, "invalid cursor");
    const { results } = await q(
      "SELECT id,sender_host,target_host,thread_id,state,attempt_id,submission_id,evidence,created_at,updated_at FROM messages WHERE target_host=? AND id>? AND state!='completed' ORDER BY id LIMIT 101",
      host,
      cursor,
    ).all();
    return json({
      messages: results.slice(0, 100),
      next_cursor: results.length > 100 ? results[99].id : "",
    });
  }
  if (request.method === "POST" && path === "/v1/claim") {
    const b = await body(request);
    requireValue(uuid.test(b.request_id), "invalid request ID");
    // One atomic batch records both empty claims and selected messages. Lost responses
    // can replay the same attempt; dispatching messages are NEVER automatically leased again.
    await db.batch([
      q(
        "INSERT INTO hosts(id,seen_at) VALUES(?,?) ON CONFLICT(id) DO NOTHING",
        host,
        now,
      ),
      q(
        "INSERT INTO attempts(host_id,request_id,message_id,created_at) SELECT ?,?,(SELECT m.id FROM messages m JOIN tasks t ON t.host_id=m.target_host AND t.thread_id=m.thread_id WHERE m.target_host=? AND m.state='queued' AND t.enabled=1 ORDER BY m.created_at,m.id LIMIT 1),? ON CONFLICT(host_id,request_id) DO NOTHING",
        host,
        b.request_id,
        host,
        now,
      ),
      q(
        "UPDATE messages SET state='dispatching',attempt_id=?,updated_at=? WHERE id=(SELECT message_id FROM attempts WHERE host_id=? AND request_id=?) AND state='queued'",
        b.request_id,
        now,
        host,
        b.request_id,
      ),
    ]);
    const m = await q(
      "SELECT m.* FROM attempts a JOIN messages m ON m.id=a.message_id WHERE a.host_id=? AND a.request_id=?",
      host,
      b.request_id,
    ).first();
    return json({ message: m });
  }
  if (request.method === "POST" && path === "/v1/receipts") {
    const b = await body(request);
    requireValue(
      uuid.test(b.request_id) &&
        text(b.message_id, 110) &&
        ["submitted", "uncertain", "acknowledged", "completed"].includes(
          b.state,
        ),
      "invalid receipt",
    );
    b.submission_id ??= "";
    b.evidence ??= "";
    requireValue(
      (b.submission_id === "" || uuid.test(b.submission_id)) &&
        typeof b.evidence === "string" &&
        new TextEncoder().encode(b.evidence).length <= 2048,
      "invalid receipt evidence",
    );
    requireValue(
      b.state !== "submitted" || uuid.test(b.submission_id),
      "submission ID required",
    );
    requireValue(
      !["acknowledged", "completed"].includes(b.state) ||
        text(b.evidence, 2048),
      "observed destination evidence required",
    );
    const existing = await q(
      "SELECT * FROM receipts WHERE host_id=? AND request_id=?",
      host,
      b.request_id,
    ).first();
    if (existing) {
      requireValue(
        ["message_id", "state", "submission_id", "evidence"].every(
          (k) => existing[k] === b[k],
        ),
        "receipt replay mismatch",
        409,
      );
      return json({ receipt: existing });
    }
    // Only the destination may report delivery. Conditional INSERT is also the
    // transition guard, inside the same transaction as its message projection.
    const allowed = {
      submitted: ["dispatching"],
      uncertain: ["dispatching"],
      acknowledged: ["submitted", "uncertain", "dispatching"],
      completed: ["acknowledged"],
    };
    const states = allowed[b.state];
    await db.batch([
      q(
        `INSERT INTO receipts(host_id,request_id,message_id,state,submission_id,evidence,created_at) SELECT ?,?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM messages WHERE id=? AND target_host=? AND state IN (${states.map(() => "?").join(",")})) ON CONFLICT(host_id,request_id) DO NOTHING`,
        host,
        b.request_id,
        b.message_id,
        b.state,
        b.submission_id,
        b.evidence,
        now,
        b.message_id,
        host,
        ...states,
      ),
      q(
        `UPDATE messages SET state=?,submission_id=CASE WHEN ?='' THEN submission_id ELSE ? END,evidence=?,updated_at=? WHERE ((?='submitted' AND state='dispatching') OR (?='uncertain' AND state='dispatching') OR (?='acknowledged' AND state IN ('dispatching','submitted','uncertain')) OR (?='completed' AND state='acknowledged')) AND id=? AND target_host=? AND EXISTS(SELECT 1 FROM receipts WHERE host_id=? AND request_id=? AND message_id=? AND state=? AND submission_id=? AND evidence=?)`,
        b.state,
        b.submission_id,
        b.submission_id,
        b.evidence,
        now,
        b.state,
        b.state,
        b.state,
        b.state,
        b.message_id,
        host,
        host,
        b.request_id,
        b.message_id,
        b.state,
        b.submission_id,
        b.evidence,
      ),
    ]);
    const r = await q(
      "SELECT * FROM receipts WHERE host_id=? AND request_id=?",
      host,
      b.request_id,
    ).first();
    requireValue(
      r &&
        ["message_id", "state", "submission_id", "evidence"].every(
          (k) => r[k] === b[k],
        ),
      "receipt not permitted for destination or current state",
      409,
    );
    return json({ receipt: r });
  }
  if (request.method === "GET" && path.startsWith("/v1/messages/")) {
    const id = decodeURIComponent(path.slice("/v1/messages/".length));
    requireValue(text(id, 110), "invalid message ID");
    const m = await q(
      "SELECT * FROM messages WHERE id=? AND (sender_host=? OR target_host=?)",
      id,
      host,
      host,
    ).first();
    requireValue(m, "message not found", 404);
    return json(m);
  }
  throw new HTTPError(404, "route not found");
}
export default {
  async fetch(request, env) {
    try {
      return await handle(request, env);
    } catch (error) {
      return json(
        {
          error:
            error instanceof HTTPError
              ? error.message
              : "coordinator operation failed",
        },
        error instanceof HTTPError ? error.status : 500,
      );
    }
  },
};
