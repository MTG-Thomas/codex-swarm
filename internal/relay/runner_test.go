package relay

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

type fakeMailbox struct {
	message               *Message
	failClaim, failReport bool
	ids                   []string
	reports               []Receipt
}

func (f *fakeMailbox) Claim(_ context.Context, id string) (*Message, error) {
	f.ids = append(f.ids, id)
	if f.failClaim {
		f.failClaim = false
		return nil, errors.New("lost response")
	}
	if f.message == nil {
		return nil, nil
	}
	m := *f.message
	m.AttemptID = id
	return &m, nil
}
func (f *fakeMailbox) Report(_ context.Context, r Receipt) error {
	f.reports = append(f.reports, r)
	if f.failReport {
		f.failReport = false
		return errors.New("offline")
	}
	return nil
}

type fakeQueue struct {
	calls int
	err   error
}

func (f *fakeQueue) Submit(context.Context, string, string) (string, error) {
	f.calls++
	return NewID(), f.err
}
func journal(t *testing.T, path string) *Journal {
	t.Helper()
	j, err := OpenJournal(path, "https://test.invalid", "windows")
	if err != nil {
		t.Fatal(err)
	}
	return j
}
func TestReceiptFailureReplaysWithoutSubmittingAgain(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "relay.db")
	j := journal(t, path)
	m := &Message{ID: "linux:" + NewID(), TargetHost: "windows", ThreadID: NewID(), Prompt: "test"}
	if err := j.Enroll(ctx, m.ThreadID, true); err != nil {
		t.Fatal(err)
	}
	box := &fakeMailbox{message: m, failReport: true}
	q := &fakeQueue{}
	r := Runner{Host: "windows", Journal: j, Mailbox: box, Queue: q}
	if err := r.Once(ctx); err == nil {
		t.Fatal("want receipt transport failure")
	}
	j.Close()
	j = journal(t, path)
	defer j.Close()
	r.Journal = j
	if err := r.Once(ctx); err != nil {
		t.Fatal(err)
	}
	if q.calls != 1 || len(box.reports) != 2 || box.reports[0] != box.reports[1] {
		t.Fatalf("duplicate queue or changed receipt: %d %#v", q.calls, box.reports)
	}
}
func TestLostClaimResponseRetainsRequestAcrossRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "relay.db")
	j := journal(t, path)
	box := &fakeMailbox{failClaim: true}
	r := Runner{Host: "windows", Journal: j, Mailbox: box, Queue: &fakeQueue{}}
	if err := r.Once(ctx); err == nil {
		t.Fatal("want lost response")
	}
	j.Close()
	j = journal(t, path)
	defer j.Close()
	r.Journal = j
	if err := r.Once(ctx); err != nil {
		t.Fatal(err)
	}
	if len(box.ids) != 2 || box.ids[0] != box.ids[1] {
		t.Fatalf("claim request changed: %v", box.ids)
	}
}
func TestRestartDuringSubmissionBecomesUncertain(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "relay.db")
	j := journal(t, path)
	m := &Message{ID: "linux:" + NewID(), TargetHost: "windows", ThreadID: NewID(), Prompt: "test"}
	e := entry{ID: NewID(), Phase: "dispatching", Message: m}
	if err := j.save(ctx, e); err != nil {
		t.Fatal(err)
	}
	j.Close()
	j = journal(t, path)
	defer j.Close()
	box := &fakeMailbox{}
	q := &fakeQueue{}
	if err := (Runner{Host: "windows", Journal: j, Mailbox: box, Queue: q}).Once(ctx); err != nil {
		t.Fatal(err)
	}
	if q.calls != 0 || len(box.reports) != 1 || box.reports[0].State != "uncertain" {
		t.Fatal("crash caused resend or false acknowledgment")
	}
}
func TestLocalEnrollmentRequired(t *testing.T) {
	ctx := context.Background()
	j := journal(t, filepath.Join(t.TempDir(), "relay.db"))
	defer j.Close()
	q := &fakeQueue{}
	box := &fakeMailbox{message: &Message{ID: "linux:" + NewID(), TargetHost: "windows", ThreadID: NewID(), Prompt: "test"}}
	if err := (Runner{Host: "windows", Journal: j, Mailbox: box, Queue: q}).Once(ctx); err != nil {
		t.Fatal(err)
	}
	if q.calls != 0 || box.reports[0].State != "uncertain" {
		t.Fatal("unenrolled task was delivered")
	}
}
func TestJournalRejectsConcurrentOwnerAndIdentityDrift(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relay.db")
	j := journal(t, path)
	if other, err := OpenJournal(path, "https://test.invalid", "windows"); err == nil {
		other.Close()
		t.Fatal("two journal owners")
	}
	j.Close()
	if other, err := OpenJournal(path, "https://other.invalid", "windows"); err == nil {
		other.Close()
		t.Fatal("journal rebound to another coordinator")
	}
}
func TestQueueFailureIsUncertain(t *testing.T) {
	ctx := context.Background()
	j := journal(t, filepath.Join(t.TempDir(), "relay.db"))
	defer j.Close()
	m := &Message{ID: "linux:" + NewID(), TargetHost: "windows", ThreadID: NewID(), Prompt: "test"}
	if err := j.Enroll(ctx, m.ThreadID, true); err != nil {
		t.Fatal(err)
	}
	q := &fakeQueue{err: errors.New("timeout")}
	box := &fakeMailbox{message: m}
	if err := (Runner{Host: "windows", Journal: j, Mailbox: box, Queue: q}).Once(ctx); err != nil {
		t.Fatal(err)
	}
	if q.calls != 1 || box.reports[0].State != "uncertain" {
		t.Fatal("failed submission marked successful")
	}
}

func (f *fakeMailbox) Ready(context.Context) (bool, error) { return true, nil }

func (f *fakeMailbox) Inspect(_ context.Context, id string) (*Message, error) {
	m := *f.message
	m.AttemptID = f.ids[len(f.ids)-1]
	if m.State == "" {
		m.State = "dispatching"
	}
	return &m, nil
}
func TestAlreadyAcknowledgedClaimNeverSubmits(t *testing.T) {
	ctx := context.Background()
	j := journal(t, filepath.Join(t.TempDir(), "relay.db"))
	defer j.Close()
	m := &Message{ID: "linux:" + NewID(), TargetHost: "windows", ThreadID: NewID(), Prompt: "test", State: "acknowledged"}
	if err := j.Enroll(ctx, m.ThreadID, true); err != nil {
		t.Fatal(err)
	}
	q := &fakeQueue{}
	box := &fakeMailbox{message: m}
	if err := (Runner{Host: "windows", Journal: j, Mailbox: box, Queue: q}).Once(ctx); err != nil {
		t.Fatal(err)
	}
	if q.calls != 0 {
		t.Fatal("already acknowledged delivery was submitted again")
	}
}
