package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestCreateMessagePrunesOnlyFullyDeliveredMessagesAtQuota(t *testing.T) {
	st := NewJSONStore(filepath.Join(t.TempDir(), "state.db"))
	seedMessages(t, st, MessageRetentionCap, func(i int) DeliveryState {
		if i == 0 {
			return DeliveryDelivered
		}
		return DeliveryQueued
	})

	message := Message{ID: "replacement", RequestID: "replacement-request", Kind: MessageDirect, From: "sender", Body: "new", CreatedAt: time.Now().UTC()}
	if _, _, _, err := st.CreateMessage(message, []string{"recipient"}); err != nil {
		t.Fatalf("CreateMessage() error = %v", err)
	}
	var count, oldest int
	if err := st.withStateLock(func() error {
		if err := st.tx.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&count); err != nil {
			return err
		}
		return st.tx.QueryRow(`SELECT COUNT(*) FROM messages WHERE id='message-0000'`).Scan(&oldest)
	}); err != nil {
		t.Fatal(err)
	}
	if count != MessageRetentionCap || oldest != 0 {
		t.Fatalf("message count=%d oldest=%d, want count=%d oldest pruned", count, oldest, MessageRetentionCap)
	}
}

func TestCreateMessageRejectsInsertWhenPendingMessagesFillQuota(t *testing.T) {
	st := NewJSONStore(filepath.Join(t.TempDir(), "state.db"))
	seedMessages(t, st, MessageRetentionCap, func(int) DeliveryState { return DeliveryQueued })
	message := Message{ID: "blocked", RequestID: "blocked-request", Kind: MessageDirect, From: "sender", Body: "new", CreatedAt: time.Now().UTC()}
	_, _, _, err := st.CreateMessage(message, []string{"recipient"})
	if !errors.Is(err, ErrMessageQuotaExceeded) {
		t.Fatalf("CreateMessage() error = %v, want ErrMessageQuotaExceeded", err)
	}
}

func TestCreateMessageRejectsFanoutBeyondDeliveryQuota(t *testing.T) {
	st := NewJSONStore(filepath.Join(t.TempDir(), "state.db"))
	recipients := make([]string, MessageDeliveryCap+1)
	for i := range recipients {
		recipients[i] = fmt.Sprintf("recipient-%04d", i)
	}
	message := Message{ID: "fanout", RequestID: "fanout-request", Kind: MessageSubtree, From: "sender", Body: "new", CreatedAt: time.Now().UTC()}
	_, _, _, err := st.CreateMessage(message, recipients)
	if !errors.Is(err, ErrMessageQuotaExceeded) {
		t.Fatalf("CreateMessage() error = %v, want ErrMessageQuotaExceeded", err)
	}
}

func TestListMessagesCapsInboxAndDeliveryHistory(t *testing.T) {
	st := NewJSONStore(filepath.Join(t.TempDir(), "state.db"))
	seedMessages(t, st, MessageInboxCap+2, func(int) DeliveryState { return DeliveryQueued })
	messages, err := st.ListMessages("recipient")
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != MessageInboxCap {
		t.Fatalf("ListMessages() length = %d, want %d", len(messages), MessageInboxCap)
	}

	message := Message{ID: "history", RequestID: "history-request", Kind: MessageDirect, From: "sender", Body: "history", CreatedAt: time.Now().UTC()}
	_, deliveries, _, err := st.CreateMessage(message, []string{"history-recipient"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < DeliveryEventCap+5; i++ {
		if err := st.UpdateDelivery(deliveries[0].ID, DeliveryQueued, fmt.Sprintf("attempt-%03d", i), time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	history, err := st.ListMessages("history-recipient")
	if err != nil {
		t.Fatal(err)
	}
	if got := len(history[0].Delivery.History); got != DeliveryEventCap {
		t.Fatalf("delivery history length = %d, want %d", got, DeliveryEventCap)
	}
}

func seedMessages(t *testing.T, st *JSONStore, count int, state func(int) DeliveryState) {
	t.Helper()
	created := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if err := st.withStateLock(func() error {
		for i := 0; i < count; i++ {
			messageID := fmt.Sprintf("message-%04d", i)
			if _, err := st.tx.Exec(`INSERT INTO messages(id,request_id,kind,sender,body,fingerprint,created_at) VALUES(?,?,?,?,?,?,?)`,
				messageID, "request-"+messageID, MessageDirect, "sender", "body", "fingerprint-"+messageID, created); err != nil {
				return err
			}
			if _, err := st.tx.Exec(`INSERT INTO message_deliveries(id,message_id,recipient_id,state,last_error,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
				"delivery-"+messageID, messageID, "recipient", state(i), "", created, created); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed messages: %v", err)
	}
}

func TestCreateMessageIsIdempotentAndListsQueuedDelivery(t *testing.T) {
	st := NewJSONStore(filepath.Join(t.TempDir(), "state.db"))
	at := time.Date(2026, 7, 21, 14, 0, 0, 0, time.UTC)
	message := Message{ID: "m-1", RequestID: "request-1", Kind: MessageDirect, From: "w-1", Body: "review this", CreatedAt: at}

	saved, deliveries, replayed, err := st.CreateMessage(message, []string{"w-2"})
	if err != nil {
		t.Fatalf("CreateMessage() error = %v", err)
	}
	if replayed || saved.ID != "m-1" || len(deliveries) != 1 || deliveries[0].State != DeliveryQueued {
		t.Fatalf("CreateMessage() = %#v %#v replayed=%t", saved, deliveries, replayed)
	}
	if len(deliveries[0].History) != 1 || deliveries[0].History[0].State != DeliveryQueued {
		t.Fatalf("CreateMessage() response history = %#v, want queued transition", deliveries[0].History)
	}
	_, replayDeliveries, replayed, err := st.CreateMessage(Message{ID: "different-id", RequestID: "request-1", Kind: MessageDirect, From: "w-1", Body: "review this", CreatedAt: at.Add(time.Minute)}, []string{"w-2"})
	if err != nil || !replayed || replayDeliveries[0].ID != deliveries[0].ID {
		t.Fatalf("replay = %#v replayed=%t err=%v", replayDeliveries, replayed, err)
	}
	queued, err := st.ListQueuedMessages("w-2")
	if err != nil || len(queued) != 1 || queued[0].Message.Body != "review this" {
		t.Fatalf("ListQueuedMessages() = %#v err=%v", queued, err)
	}
	if err := st.UpdateDelivery(deliveries[0].ID, DeliveryDelivered, "", at.Add(time.Minute)); err != nil {
		t.Fatalf("UpdateDelivery() error = %v", err)
	}
	if err := st.UpdateDelivery(deliveries[0].ID, DeliveryDelivered, "", at.Add(2*time.Minute)); err != nil {
		t.Fatalf("idempotent UpdateDelivery() error = %v", err)
	}
	queued, err = st.ListQueuedMessages("w-2")
	if err != nil || len(queued) != 0 {
		t.Fatalf("queued after delivery = %#v err=%v", queued, err)
	}
	all, err := st.ListMessages("w-2")
	if err != nil {
		t.Fatal(err)
	}
	if got := all[0].Delivery.History; len(got) != 2 || got[0].State != DeliveryQueued || got[1].State != DeliveryDelivered {
		t.Fatalf("delivery history = %#v, want queued then delivered exactly once", got)
	}
}

func TestListAllQueuedMessagesIncludesEveryRecipient(t *testing.T) {
	st := NewJSONStore(filepath.Join(t.TempDir(), "state.db"))
	at := time.Date(2026, 7, 22, 15, 0, 0, 0, time.UTC)
	_, deliveries, _, err := st.CreateMessage(Message{ID: "m-all", RequestID: "request-all", Kind: MessageSubtree, From: "w-parent", Body: "continue", CreatedAt: at}, []string{"w-one", "w-two"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateDelivery(deliveries[0].ID, DeliveryDelivered, "", at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	queued, err := st.ListAllQueuedMessages()
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 || queued[0].Delivery.RecipientID != "w-two" || queued[0].Message.ID != "m-all" {
		t.Fatalf("ListAllQueuedMessages() = %#v, want only w-two delivery", queued)
	}
	if len(queued[0].Delivery.History) != 1 || queued[0].Delivery.History[0].State != DeliveryQueued {
		t.Fatalf("queued history = %#v", queued[0].Delivery.History)
	}
}

func TestListAllMessagesIncludesEveryDeliveryAndHistory(t *testing.T) {
	st := NewJSONStore(filepath.Join(t.TempDir(), "state.db"))
	now := time.Date(2026, 7, 22, 18, 0, 0, 0, time.UTC)
	_, deliveries, _, err := st.CreateMessage(Message{ID: "m-all", RequestID: "r-all", Kind: MessageDirect, From: "w-one", Body: "hello", CreatedAt: now}, []string{"w-two", "w-three"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateDelivery(deliveries[0].ID, DeliveryDelivered, "", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	all, err := st.ListAllMessages()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || len(all[0].Delivery.History) == 0 || len(all[1].Delivery.History) == 0 {
		t.Fatalf("ListAllMessages() = %#v", all)
	}
}

func TestUpdateDeliveryRecordsFailureAndRecoveryTransitions(t *testing.T) {
	st := NewJSONStore(filepath.Join(t.TempDir(), "state.db"))
	at := time.Date(2026, 7, 21, 14, 0, 0, 0, time.UTC)
	_, deliveries, _, err := st.CreateMessage(Message{ID: "m-1", RequestID: "request-1", Kind: MessageDirect, From: "w-1", Body: "review", CreatedAt: at}, []string{"w-2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateDelivery(deliveries[0].ID, DeliveryQueued, "stale turn", at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateDelivery(deliveries[0].ID, DeliverySteered, "", at.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	all, err := st.ListMessages("w-2")
	if err != nil {
		t.Fatal(err)
	}
	history := all[0].Delivery.History
	if len(history) != 3 || history[1].LastError != "stale turn" || history[2].State != DeliverySteered {
		t.Fatalf("delivery history = %#v", history)
	}
}

func TestCreateMessageRejectsMismatchedReplay(t *testing.T) {
	st := NewJSONStore(filepath.Join(t.TempDir(), "state.db"))
	message := Message{ID: "m-1", RequestID: "request-1", Kind: MessageDirect, From: "w-1", Body: "first", CreatedAt: time.Now().UTC()}
	if _, _, _, err := st.CreateMessage(message, []string{"w-2"}); err != nil {
		t.Fatal(err)
	}
	message.ID = "m-2"
	message.Body = "second"
	_, _, _, err := st.CreateMessage(message, []string{"w-2"})
	if !errors.Is(err, ErrMessageReplayMismatch) {
		t.Fatalf("CreateMessage() error = %v, want ErrMessageReplayMismatch", err)
	}
}

func TestRecordFileTouchWarnsOnlyForOverlappingPeerWrites(t *testing.T) {
	st := NewJSONStore(filepath.Join(t.TempDir(), "state.db"))
	at := time.Date(2026, 7, 21, 14, 0, 0, 0, time.UTC)
	first := FileTouch{ID: "t-1", WorkerID: "w-1", Repo: `C:\repo`, Path: `C:\repo\main.go`, Operation: "write", LineStart: 10, LineEnd: 20, CreatedAt: at}
	if conflicts, err := st.RecordFileTouch(first); err != nil || len(conflicts) != 0 {
		t.Fatalf("first touch conflicts = %#v err=%v", conflicts, err)
	}
	nonOverlap := FileTouch{ID: "t-2", WorkerID: "w-2", Repo: `C:\repo`, Path: `C:\repo\main.go`, Operation: "write", LineStart: 30, LineEnd: 40, CreatedAt: at.Add(time.Minute)}
	if conflicts, err := st.RecordFileTouch(nonOverlap); err != nil || len(conflicts) != 0 {
		t.Fatalf("non-overlap conflicts = %#v err=%v", conflicts, err)
	}
	overlap := FileTouch{ID: "t-3", WorkerID: "w-3", Repo: `C:\repo`, Path: `C:\repo\main.go`, Operation: "write", LineStart: 15, LineEnd: 16, CreatedAt: at.Add(2 * time.Minute)}
	conflicts, err := st.RecordFileTouch(overlap)
	if err != nil {
		t.Fatalf("overlap touch error = %v", err)
	}
	if len(conflicts) != 1 || conflicts[0].PeerTouch.WorkerID != "w-1" {
		t.Fatalf("overlap conflicts = %#v, want latest overlapping peer w-1", conflicts)
	}
}

func TestPathKeyNormalizesWindowsPathsIndependentOfHost(t *testing.T) {
	tests := map[string]string{
		`C:\Repo\.\Sub`:                "c:/repo/sub",
		`c:/repo/Other/../Sub`:         "c:/repo/sub",
		`C:\`:                          "c:/",
		`C:`:                           "c:.",
		`C:\..\Repo`:                   "c:/repo",
		`\\Server\Share\Repo`:          "//server/share/repo",
		`\\Server\Share\`:              "//server/share/",
		`\\Server\Share`:               "//server/share",
		`\\Server\Share\..\Repo`:       "//server/share/repo",
		`//SERVER/SHARE/Repo/..`:       "//server/share/",
		`\\?\UNC\Server\Share\`:        "//?/unc/server/share/",
		`\\?\UNC\Server\Share\..\Repo`: "//?/unc/server/share/repo",
	}
	for input, want := range tests {
		if got := pathKey(input); got != want {
			t.Errorf("pathKey(%q) = %q; want %q", input, got, want)
		}
	}
}
