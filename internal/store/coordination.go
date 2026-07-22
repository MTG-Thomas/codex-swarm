package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const touchRetention = 30 * time.Minute

// CreateMessage inserts an immutable message and one queued delivery per recipient.
// Reusing a request ID is idempotent only when the complete message matches.
func (s *JSONStore) CreateMessage(message Message, recipients []string) (Message, []Delivery, bool, error) {
	recipients = uniqueNonEmpty(recipients)
	if strings.TrimSpace(message.RequestID) == "" {
		return Message{}, nil, false, errors.New("message request id is required")
	}
	if strings.TrimSpace(message.ID) == "" {
		return Message{}, nil, false, errors.New("message id is required")
	}
	if strings.TrimSpace(message.Body) == "" {
		return Message{}, nil, false, errors.New("message body is required")
	}
	if len(recipients) == 0 {
		return Message{}, nil, false, errors.New("message requires at least one recipient")
	}
	fingerprint := messageFingerprint(message, recipients)
	var saved Message
	var deliveries []Delivery
	replayed := false
	err := s.withStateLock(func() error {
		var existingFingerprint string
		row := s.tx.QueryRow(`SELECT id, kind, sender, body, created_at, fingerprint FROM messages WHERE request_id = ?`, message.RequestID)
		var created string
		err := row.Scan(&saved.ID, &saved.Kind, &saved.From, &saved.Body, &created, &existingFingerprint)
		if err == nil {
			saved.RequestID = message.RequestID
			if existingFingerprint != fingerprint {
				return fmt.Errorf("request %q for message does not match original mutation fingerprint: %w", message.RequestID, ErrMessageReplayMismatch)
			}
			saved.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
			if err != nil {
				return fmt.Errorf("parse message created_at: %w", err)
			}
			deliveries, err = listMessageDeliveries(s.tx, saved.ID)
			replayed = true
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("find message request %s: %w", message.RequestID, err)
		}

		createdAt := message.CreatedAt.UTC()
		if createdAt.IsZero() {
			createdAt = time.Now().UTC()
		}
		message.CreatedAt = createdAt
		if _, err := s.tx.Exec(`INSERT INTO messages(id,request_id,kind,sender,body,fingerprint,created_at) VALUES(?,?,?,?,?,?,?)`,
			message.ID, message.RequestID, message.Kind, message.From, message.Body, fingerprint, createdAt.Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("insert message %s: %w", message.ID, err)
		}
		for i, recipient := range recipients {
			delivery := Delivery{
				ID:          fmt.Sprintf("%s-d%03d", message.ID, i+1),
				MessageID:   message.ID,
				RecipientID: recipient,
				State:       DeliveryQueued,
				CreatedAt:   createdAt,
				UpdatedAt:   createdAt,
			}
			if _, err := s.tx.Exec(`INSERT INTO message_deliveries(id,message_id,recipient_id,state,last_error,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
				delivery.ID, delivery.MessageID, delivery.RecipientID, delivery.State, "", createdAt.Format(time.RFC3339Nano), createdAt.Format(time.RFC3339Nano)); err != nil {
				return fmt.Errorf("insert message delivery %s: %w", delivery.ID, err)
			}
			eventResult, err := s.tx.Exec(`INSERT INTO message_delivery_events(delivery_id,state,last_error,created_at) VALUES(?,?,?,?)`,
				delivery.ID, delivery.State, "", createdAt.Format(time.RFC3339Nano))
			if err != nil {
				return fmt.Errorf("insert message delivery event %s: %w", delivery.ID, err)
			}
			sequence, err := eventResult.LastInsertId()
			if err != nil {
				return fmt.Errorf("read message delivery event sequence %s: %w", delivery.ID, err)
			}
			delivery.History = []DeliveryEvent{{Sequence: sequence, DeliveryID: delivery.ID, State: delivery.State, CreatedAt: createdAt}}
			deliveries = append(deliveries, delivery)
		}
		saved = message
		return nil
	})
	return saved, deliveries, replayed, err
}

// ListMessages returns a recipient's messages newest first.
func (s *JSONStore) ListMessages(recipientID string) ([]DeliveredMessage, error) {
	var messages []DeliveredMessage
	err := s.withStateLock(func() (err error) {
		rows, err := s.tx.Query(`SELECT m.id,m.request_id,m.kind,m.sender,m.body,m.created_at,
			d.id,d.state,d.last_error,d.created_at,d.updated_at
			FROM message_deliveries d JOIN messages m ON m.id=d.message_id
			WHERE d.recipient_id=? ORDER BY m.created_at DESC`, recipientID)
		if err != nil {
			return fmt.Errorf("list messages for %s: %w", recipientID, err)
		}
		for rows.Next() {
			var item DeliveredMessage
			var messageCreated, deliveryCreated, updated string
			if err := rows.Scan(&item.Message.ID, &item.Message.RequestID, &item.Message.Kind, &item.Message.From, &item.Message.Body, &messageCreated,
				&item.Delivery.ID, &item.Delivery.State, &item.Delivery.LastError, &deliveryCreated, &updated); err != nil {
				return err
			}
			item.Delivery.MessageID = item.Message.ID
			item.Delivery.RecipientID = recipientID
			if item.Message.CreatedAt, err = time.Parse(time.RFC3339Nano, messageCreated); err != nil {
				return err
			}
			if item.Delivery.CreatedAt, err = time.Parse(time.RFC3339Nano, deliveryCreated); err != nil {
				return err
			}
			if item.Delivery.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
				return err
			}
			messages = append(messages, item)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for i := range messages {
			messages[i].Delivery.History, err = listDeliveryEvents(s.tx, messages[i].Delivery.ID)
			if err != nil {
				return err
			}
		}
		return nil
	})
	return messages, err
}

// ListAllMessages returns every durable message delivery newest first. A
// message with multiple recipients appears once per delivery because delivery
// state is recipient-specific.
func (s *JSONStore) ListAllMessages() ([]DeliveredMessage, error) {
	var messages []DeliveredMessage
	err := s.withStateLock(func() error {
		var err error
		messages, err = listAllMessages(s.tx)
		return err
	})
	return messages, err
}

func listAllMessages(q sqlExecutor) (messages []DeliveredMessage, err error) {
	rows, err := q.Query(`SELECT m.id,m.request_id,m.kind,m.sender,m.body,m.created_at,
			d.id,d.recipient_id,d.state,d.last_error,d.created_at,d.updated_at
			FROM message_deliveries d JOIN messages m ON m.id=d.message_id
			ORDER BY m.created_at DESC,d.id`)
	if err != nil {
		return nil, fmt.Errorf("list all messages: %w", err)
	}
	for rows.Next() {
		var item DeliveredMessage
		var messageCreated, deliveryCreated, updated string
		if err := rows.Scan(&item.Message.ID, &item.Message.RequestID, &item.Message.Kind, &item.Message.From, &item.Message.Body, &messageCreated,
			&item.Delivery.ID, &item.Delivery.RecipientID, &item.Delivery.State, &item.Delivery.LastError, &deliveryCreated, &updated); err != nil {
			_ = rows.Close()
			return nil, err
		}
		item.Delivery.MessageID = item.Message.ID
		if item.Message.CreatedAt, err = time.Parse(time.RFC3339Nano, messageCreated); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if item.Delivery.CreatedAt, err = time.Parse(time.RFC3339Nano, deliveryCreated); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if item.Delivery.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
			_ = rows.Close()
			return nil, err
		}
		messages = append(messages, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range messages {
		messages[i].Delivery.History, err = listDeliveryEvents(q, messages[i].Delivery.ID)
		if err != nil {
			return nil, err
		}
	}
	return messages, nil
}

// ListQueuedMessages returns the messages waiting for a worker's next turn.
func (s *JSONStore) ListQueuedMessages(recipientID string) ([]DeliveredMessage, error) {
	all, err := s.ListMessages(recipientID)
	if err != nil {
		return nil, err
	}
	queued := make([]DeliveredMessage, 0, len(all))
	for _, item := range all {
		if item.Delivery.State == DeliveryQueued {
			queued = append(queued, item)
		}
	}
	sort.Slice(queued, func(i, j int) bool { return queued[i].Message.CreatedAt.Before(queued[j].Message.CreatedAt) })
	return queued, nil
}

// ListAllQueuedMessages returns every delivery waiting for a worker's next
// turn. It is intended for read-only operator projections such as attention.
func (s *JSONStore) ListAllQueuedMessages() ([]DeliveredMessage, error) {
	var messages []DeliveredMessage
	err := s.withStateLock(func() (err error) {
		rows, err := s.tx.Query(`SELECT m.id,m.request_id,m.kind,m.sender,m.body,m.created_at,
			d.id,d.recipient_id,d.state,d.last_error,d.created_at,d.updated_at
			FROM message_deliveries d JOIN messages m ON m.id=d.message_id
			WHERE d.state=? ORDER BY m.created_at`, DeliveryQueued)
		if err != nil {
			return fmt.Errorf("list all queued messages: %w", err)
		}
		for rows.Next() {
			var item DeliveredMessage
			var messageCreated, deliveryCreated, updated string
			if err := rows.Scan(&item.Message.ID, &item.Message.RequestID, &item.Message.Kind, &item.Message.From, &item.Message.Body, &messageCreated,
				&item.Delivery.ID, &item.Delivery.RecipientID, &item.Delivery.State, &item.Delivery.LastError, &deliveryCreated, &updated); err != nil {
				return err
			}
			item.Delivery.MessageID = item.Message.ID
			if item.Message.CreatedAt, err = time.Parse(time.RFC3339Nano, messageCreated); err != nil {
				return err
			}
			if item.Delivery.CreatedAt, err = time.Parse(time.RFC3339Nano, deliveryCreated); err != nil {
				return err
			}
			if item.Delivery.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
				return err
			}
			messages = append(messages, item)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for i := range messages {
			messages[i].Delivery.History, err = listDeliveryEvents(s.tx, messages[i].Delivery.ID)
			if err != nil {
				return err
			}
		}
		return nil
	})
	return messages, err
}

// UpdateDelivery records a delivery attempt without deleting its durable message.
func (s *JSONStore) UpdateDelivery(id string, state DeliveryState, lastError string, at time.Time) error {
	if state != DeliveryQueued && state != DeliverySteered && state != DeliveryDelivered {
		return fmt.Errorf("unsupported delivery state %q", state)
	}
	return s.withStateLock(func() error {
		var currentState DeliveryState
		var currentError string
		if err := s.tx.QueryRow(`SELECT state,last_error FROM message_deliveries WHERE id=?`, id).Scan(&currentState, &currentError); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("delivery not found: %s", id)
			}
			return fmt.Errorf("read delivery %s: %w", id, err)
		}
		if currentState == state && currentError == lastError {
			return nil
		}
		updatedAt := at.UTC()
		result, err := s.tx.Exec(`UPDATE message_deliveries SET state=?,last_error=?,updated_at=? WHERE id=?`, state, lastError, at.UTC().Format(time.RFC3339Nano), id)
		if err != nil {
			return fmt.Errorf("update delivery %s: %w", id, err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			return fmt.Errorf("delivery not found: %s", id)
		}
		if _, err := s.tx.Exec(`INSERT INTO message_delivery_events(delivery_id,state,last_error,created_at) VALUES(?,?,?,?)`,
			id, state, lastError, updatedAt.Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("insert delivery event %s: %w", id, err)
		}
		return nil
	})
}

// RecordFileTouch stores a recent touch and returns warning-only write conflicts.
func (s *JSONStore) RecordFileTouch(touch FileTouch) ([]TouchConflict, error) {
	if strings.TrimSpace(touch.ID) == "" || strings.TrimSpace(touch.WorkerID) == "" {
		return nil, errors.New("touch id and worker id are required")
	}
	if touch.Operation != "read" && touch.Operation != "write" {
		return nil, fmt.Errorf("unsupported touch operation %q", touch.Operation)
	}
	if touch.LineStart < 0 || touch.LineEnd < 0 || (touch.LineEnd > 0 && touch.LineEnd < touch.LineStart) {
		return nil, errors.New("invalid touch line range")
	}
	createdAt := touch.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	touch.CreatedAt = createdAt
	repoKey := pathKey(touch.Repo)
	fileKey := pathKey(touch.Path)
	var conflicts []TouchConflict
	err := s.withStateLock(func() (err error) {
		cutoff := createdAt.Add(-touchRetention).Format(time.RFC3339Nano)
		if _, err := s.tx.Exec(`DELETE FROM file_touches WHERE created_at < ?`, cutoff); err != nil {
			return fmt.Errorf("prune file touches: %w", err)
		}
		if touch.Operation == "write" {
			rows, err := s.tx.Query(`SELECT id,worker_id,repo,path,operation,line_start,line_end,intent,created_at
				FROM file_touches WHERE repo_key=? AND path_key=? AND operation='write' AND worker_id<>? AND created_at>=?
				ORDER BY created_at DESC LIMIT 100`, repoKey, fileKey, touch.WorkerID, cutoff)
			if err != nil {
				return fmt.Errorf("query file touch conflicts: %w", err)
			}
			seen := map[string]struct{}{}
			for rows.Next() {
				var peer FileTouch
				var at string
				if err := rows.Scan(&peer.ID, &peer.WorkerID, &peer.Repo, &peer.Path, &peer.Operation, &peer.LineStart, &peer.LineEnd, &peer.Intent, &at); err != nil {
					_ = rows.Close()
					return err
				}
				peer.CreatedAt, err = time.Parse(time.RFC3339Nano, at)
				if err != nil {
					_ = rows.Close()
					return err
				}
				if _, ok := seen[peer.WorkerID]; ok || !lineRangesOverlap(touch, peer) {
					continue
				}
				seen[peer.WorkerID] = struct{}{}
				conflicts = append(conflicts, TouchConflict{Touch: touch, PeerTouch: peer})
			}
			if err := rows.Close(); err != nil {
				return err
			}
			if err := rows.Err(); err != nil {
				return err
			}
		}
		_, err = s.tx.Exec(`INSERT OR IGNORE INTO file_touches(id,worker_id,repo,repo_key,path,path_key,operation,line_start,line_end,intent,created_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`, touch.ID, touch.WorkerID, touch.Repo, repoKey, touch.Path, fileKey, touch.Operation,
			touch.LineStart, touch.LineEnd, touch.Intent, createdAt.Format(time.RFC3339Nano))
		if err != nil {
			return fmt.Errorf("insert file touch %s: %w", touch.ID, err)
		}
		return nil
	})
	return conflicts, err
}

func listMessageDeliveries(q sqlExecutor, messageID string) (deliveries []Delivery, err error) {
	rows, err := q.Query(`SELECT id,recipient_id,state,last_error,created_at,updated_at FROM message_deliveries WHERE message_id=? ORDER BY id`, messageID)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var delivery Delivery
		var created, updated string
		if err := rows.Scan(&delivery.ID, &delivery.RecipientID, &delivery.State, &delivery.LastError, &created, &updated); err != nil {
			return nil, err
		}
		delivery.MessageID = messageID
		if delivery.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, err
		}
		if delivery.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
			return nil, err
		}
		deliveries = append(deliveries, delivery)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range deliveries {
		deliveries[i].History, err = listDeliveryEvents(q, deliveries[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return deliveries, nil
}

func listDeliveryEvents(q sqlExecutor, deliveryID string) (events []DeliveryEvent, err error) {
	rows, err := q.Query(`SELECT sequence,state,last_error,created_at FROM message_delivery_events WHERE delivery_id=? ORDER BY sequence`, deliveryID)
	if err != nil {
		return nil, fmt.Errorf("list delivery events for %s: %w", deliveryID, err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var event DeliveryEvent
		var created string
		if err := rows.Scan(&event.Sequence, &event.State, &event.LastError, &created); err != nil {
			return nil, err
		}
		event.DeliveryID = deliveryID
		if event.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func uniqueNonEmpty(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func messageFingerprint(message Message, recipients []string) string {
	values := append([]string(nil), recipients...)
	sort.Strings(values)
	sum := sha256.Sum256([]byte(string(message.Kind) + "\x00" + message.From + "\x00" + message.Body + "\x00" + strings.Join(values, "\x00")))
	return hex.EncodeToString(sum[:])
}

func pathKey(value string) string {
	key := filepath.ToSlash(filepath.Clean(strings.TrimSpace(value)))
	if runtime.GOOS == "windows" {
		return strings.ToLower(key)
	}
	return key
}

func lineRangesOverlap(left, right FileTouch) bool {
	if left.LineStart == 0 || left.LineEnd == 0 || right.LineStart == 0 || right.LineEnd == 0 {
		return true
	}
	return left.LineStart <= right.LineEnd && right.LineStart <= left.LineEnd
}
