package relay

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Journal is separate from the local swarm ledger. It contains only local
// enrollment, pending claim requests, and durable queue submission receipts.
type Journal struct {
	db   *sql.DB
	lock *os.File
}
type entry struct {
	ID, Phase string
	Message   *Message
	Receipt   Receipt
}

func OpenJournal(path, origin, host string) (*Journal, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	ok, err := tryLockStateFile(lock)
	if err != nil || !ok {
		lock.Close()
		return nil, fmt.Errorf("relay journal is busy or unavailable: %s", path)
	}
	j := &Journal{lock: lock}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		j.Close()
		return nil, err
	}
	f.Close()
	j.db, err = sql.Open("sqlite", path)
	if err != nil {
		j.Close()
		return nil, err
	}
	j.db.SetMaxOpenConns(1)
	_, err = j.db.Exec(`PRAGMA busy_timeout=5000; PRAGMA journal_mode=DELETE; PRAGMA synchronous=FULL;
 CREATE TABLE IF NOT EXISTS identity (singleton INTEGER PRIMARY KEY CHECK(singleton=1), origin TEXT NOT NULL, host TEXT NOT NULL, os_user TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS enrolled (thread TEXT PRIMARY KEY, enabled INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS pending (id TEXT PRIMARY KEY, phase TEXT NOT NULL, message TEXT NOT NULL, receipt TEXT NOT NULL);`)
	if err != nil {
		j.Close()
		return nil, err
	}
	u, err := user.Current()
	if err != nil {
		j.Close()
		return nil, err
	}
	_, err = j.db.Exec("INSERT INTO identity(singleton,origin,host,os_user) VALUES(1,?,?,?) ON CONFLICT DO NOTHING", origin, host, u.Uid)
	if err != nil {
		j.Close()
		return nil, err
	}
	var o, h, id string
	err = j.db.QueryRow("SELECT origin,host,os_user FROM identity WHERE singleton=1").Scan(&o, &h, &id)
	if err != nil || o != origin || h != host || id != u.Uid {
		j.Close()
		return nil, fmt.Errorf("journal belongs to a different coordinator, host or OS user")
	}
	return j, nil
}
func (j *Journal) Close() error {
	var err error
	if j.db != nil {
		err = j.db.Close()
	}
	if j.lock != nil {
		_ = unlockStateFile(j.lock)
		_ = j.lock.Close()
	}
	return err
}
func (j *Journal) Enroll(ctx context.Context, thread string, enabled bool) error {
	if !uuidPattern.MatchString(thread) {
		return fmt.Errorf("task must be an exact UUID")
	}
	_, err := j.db.ExecContext(ctx, "INSERT INTO enrolled(thread,enabled) VALUES(?,?) ON CONFLICT(thread) DO UPDATE SET enabled=excluded.enabled", thread, enabled)
	return err
}
func (j *Journal) allowed(ctx context.Context, thread string) bool {
	var enabled bool
	return j.db.QueryRowContext(ctx, "SELECT enabled FROM enrolled WHERE thread=?", thread).Scan(&enabled) == nil && enabled
}
func (j *Journal) next(ctx context.Context) (entry, error) {
	var e entry
	var m, r string
	err := j.db.QueryRowContext(ctx, "SELECT id,phase,message,receipt FROM pending WHERE phase!='done' ORDER BY rowid LIMIT 1").Scan(&e.ID, &e.Phase, &m, &r)

	if err != nil {
		return e, err
	}
	if err = json.Unmarshal([]byte(m), &e.Message); err != nil {
		return e, err
	}
	err = json.Unmarshal([]byte(r), &e.Receipt)
	return e, err
}
func (j *Journal) save(ctx context.Context, e entry) error {
	m, err := json.Marshal(e.Message)
	if err != nil {
		return err
	}
	r, err := json.Marshal(e.Receipt)
	if err != nil {
		return err
	}
	_, err = j.db.ExecContext(ctx, "INSERT INTO pending(id,phase,message,receipt) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET phase=excluded.phase,message=excluded.message,receipt=excluded.receipt", e.ID, e.Phase, string(m), string(r))
	return err
}
