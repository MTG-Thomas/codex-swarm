// Exercises the actual Go commands against a real local D1 Worker runtime.
// The fake Codex executable only emits a receipt; it never opens a real task.
import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, writeFile, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { execFile, spawn } from "node:child_process";
import { promisify } from "node:util";
import { randomUUID, createHash } from "node:crypto";
import { Miniflare } from "miniflare";
const run = promisify(execFile);
test(
  "Go sender and receiver deliver once through D1 and retain separate acknowledgment",
  { skip: process.platform === "win32" || process.getuid?.() === 0 },
  async () => {
    const dir = await mkdtemp(join(tmpdir(), "swarm-relay-"));
    const root = resolve("..");
    let mf, daemon;
    try {
      const cs = join(dir, "cs"),
        csd = join(dir, "csd");
      await run("go", ["build", "-o", cs, "./cmd/cs"], { cwd: root });
      await run("go", ["build", "-o", csd, "./cmd/csd"], { cwd: root });
      const token = "local-test-token-not-a-real-secret";
      const hash = createHash("sha256").update(token).digest("hex");
      mf = new Miniflare({
        modules: true,
        scriptPath: resolve("src/index.js"),
        compatibilityDate: "2026-07-30",
        d1Databases: ["DB"],
        bindings: {
          HOST_KEYS_JSON: JSON.stringify({ sender: hash, receiver: hash }),
        },
      });
      const db = await mf.getD1Database("DB");
      const sql = await readFile("migrations/0001_relay.sql", "utf8");
      await db.batch(
        sql
          .split(";")
          .map((s) => s.trim())
          .filter(Boolean)
          .map((s) => db.prepare(s)),
      );
      const url = await mf.ready;
      const env = {
        ...process.env,
        CODEX_SWARM_RELAY_URL: url.origin,
        CODEX_SWARM_HOST_ID: "receiver",
        CODEX_SWARM_RELAY_TOKEN: token,
      };
      const thread = randomUUID(),
        journal = join(dir, "relay.db"),
        fake = join(dir, "codex"),
        count = join(dir, "calls");
      await writeFile(
        fake,
        `#!/bin/sh\nif [ "$2" = "--help" ]; then printf '%s\\n' 'queue --thread --message'; exit 0; fi\nprintf '%s\\n' called >> "$SWARM_TEST_CALLS"\nprintf 'Queued message 11111111-1111-4111-8111-111111111111 for thread %s.\\n' "$3"\n`,
        { mode: 0o700 },
      );
      const cli = async (args, host = "receiver") =>
        JSON.parse(
          (
            await run(cs, ["relay", ...args], {
              env: { ...env, CODEX_SWARM_HOST_ID: host },
            })
          ).stdout,
        );
      await cli([
        "register",
        "--thread",
        thread,
        "--title",
        "Integration fixture",
        "--journal",
        journal,
      ]);
      const file = join(dir, "message.txt");
      await writeFile(file, "bounded integration fixture");
      const request = randomUUID();
      const args = [
        "send",
        "--request-id",
        request,
        "--host",
        "receiver",
        "--thread",
        thread,
        "--message-file",
        file,
      ];
      const sent = await cli(args, "sender");
      assert.equal(sent.state, "queued");
      assert.equal((await cli(args, "sender")).id, sent.id);
      const poll = () =>
        run(csd, ["relay", "--once", "--journal", journal, "--codex", fake], {
          env: { ...env, SWARM_TEST_CALLS: count },
        });
      const config = join(dir, "host.json");
      await writeFile(
        config,
        JSON.stringify({
          url: url.origin,
          host: "receiver",
          token,
          codex: fake,
          journal,
          interval: "5s",
        }),
        { mode: 0o600 },
      );
      daemon = spawn(
        csd,
        [
          "serve",
          "--addr",
          "127.0.0.1:0",
          "--state",
          join(dir, "state.db"),
          "--relay-config",
          config,
        ],
        { env: { ...env, SWARM_TEST_CALLS: count }, stdio: "pipe" },
      );
      let output = "";
      daemon.stdout.on("data", (chunk) => {
        output += chunk;
      });
      const deadline = Date.now() + 15000;
      while (
        (await cli(["get", "--id", sent.id], "sender")).state !== "submitted"
      ) {
        assert.ok(Date.now() < deadline, "integrated daemon did not submit");
        assert.equal(daemon.exitCode, null, "daemon exited early");
        await new Promise((resolve) => setTimeout(resolve, 50));
      }
      const address = output.match(/addr=(127\.0\.0\.1:\d+)/)?.[1];
      assert.ok(address, "daemon listener missing");
      assert.ok(
        (await fetch(`http://${address}/healthz`)).ok,
        "local API unavailable alongside relay",
      );
      const exited = new Promise((resolve) => daemon.once("exit", resolve));
      daemon.kill("SIGTERM");
      assert.equal(await exited, 0, "integrated shutdown failed");
      daemon = null;
      // Restart through the diagnostic poller: persisted journal prevents replay.
      await poll();
      assert.equal((await readFile(count, "utf8")).trim(), "called");
      const submitted = await cli(["get", "--id", sent.id], "sender");
      assert.equal(submitted.state, "submitted");
      await cli([
        "receipt",
        "--id",
        sent.id,
        "--request-id",
        randomUUID(),
        "--status",
        "acknowledged",
        "--evidence",
        "test observer confirms receipt",
      ]);
      await cli([
        "receipt",
        "--id",
        sent.id,
        "--request-id",
        randomUUID(),
        "--status",
        "completed",
        "--evidence",
        "test observer confirms result",
      ]);
      assert.equal(
        (await cli(["get", "--id", sent.id], "sender")).state,
        "completed",
      );
    } finally {
      daemon?.kill("SIGKILL");
      await mf?.dispose();
      await rm(dir, { recursive: true, force: true });
    }
  },
);
