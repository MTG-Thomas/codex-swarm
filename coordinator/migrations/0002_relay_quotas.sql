CREATE INDEX tasks_host_quota ON tasks(host_id);
CREATE INDEX claims_host_quota ON claims(host_id, released_at, created_at);
CREATE INDEX messages_sender_quota ON messages(sender_host, state, updated_at);
CREATE INDEX attempts_host_quota ON attempts(host_id, created_at);
CREATE INDEX receipts_message_cleanup ON receipts(message_id);
