package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

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
	queued, err = st.ListQueuedMessages("w-2")
	if err != nil || len(queued) != 0 {
		t.Fatalf("queued after delivery = %#v err=%v", queued, err)
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
