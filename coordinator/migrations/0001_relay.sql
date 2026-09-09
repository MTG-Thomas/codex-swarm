CREATE TABLE hosts (
  id TEXT PRIMARY KEY,
  seen_at TEXT NOT NULL
);
CREATE TABLE tasks (
  host_id TEXT NOT NULL REFERENCES hosts(id),
  thread_id TEXT NOT NULL,
  title TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  updated_at TEXT NOT NULL,
  PRIMARY KEY(host_id, thread_id)
);
CREATE TABLE messages (
  id TEXT PRIMARY KEY,
  sender_host TEXT NOT NULL REFERENCES hosts(id),
  request_id TEXT NOT NULL,
  target_host TEXT NOT NULL,
  thread_id TEXT NOT NULL,
  prompt TEXT NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('queued','dispatching','submitted','uncertain','acknowledged','completed')),
  attempt_id TEXT,
  submission_id TEXT,
  evidence TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(sender_host, request_id),
  FOREIGN KEY(target_host, thread_id) REFERENCES tasks(host_id, thread_id)
);
CREATE INDEX messages_inbox ON messages(target_host, state, created_at, id);
CREATE TABLE attempts (
  host_id TEXT NOT NULL REFERENCES hosts(id),
  request_id TEXT NOT NULL,
  message_id TEXT REFERENCES messages(id),
  created_at TEXT NOT NULL,
  PRIMARY KEY(host_id, request_id)
);
CREATE TABLE receipts (
  host_id TEXT NOT NULL,
  request_id TEXT NOT NULL,
  message_id TEXT NOT NULL REFERENCES messages(id),
  state TEXT NOT NULL,
  submission_id TEXT NOT NULL,
  evidence TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY(host_id, request_id)
);
CREATE TABLE claims (
  id TEXT PRIMARY KEY,
  host_id TEXT NOT NULL REFERENCES hosts(id),
  resource TEXT NOT NULL,
  note TEXT NOT NULL,
  created_at TEXT NOT NULL,
  released_at TEXT
);
CREATE INDEX claims_resource ON claims(resource, released_at, id);
