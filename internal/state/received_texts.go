package state

import "time"

// maxReceivedTexts is how many received texts are kept. The oldest are dropped
// as new ones arrive, so the inbox cannot grow without bound.
const maxReceivedTexts = 100

// ReceivedText is a text that arrived on the Send text port.
type ReceivedText struct {
	ID     int64
	Sender string
	Body   string
	At     time.Time
}

// AddReceivedText saves a received text and drops all but the newest
// maxReceivedTexts. It returns the new text's ID.
func (s *StateStore) AddReceivedText(sender, body string, at time.Time) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO received_texts(sender, body, received_at) VALUES (?, ?, ?)`,
		sender, body, at.UnixNano())
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM received_texts WHERE id NOT IN
        (SELECT id FROM received_texts ORDER BY id DESC LIMIT ?)`, maxReceivedTexts); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// ReceivedTexts returns the saved texts, newest first.
func (s *StateStore) ReceivedTexts() ([]ReceivedText, error) {
	rows, err := s.db.Query(`SELECT id, sender, body, received_at FROM received_texts ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var texts []ReceivedText
	for rows.Next() {
		var t ReceivedText
		var at int64
		if err := rows.Scan(&t.ID, &t.Sender, &t.Body, &at); err != nil {
			return nil, err
		}
		t.At = time.Unix(0, at)
		texts = append(texts, t)
	}
	return texts, rows.Err()
}

// DeleteReceivedText removes one saved text. Deleting an ID that is gone is not
// an error.
func (s *StateStore) DeleteReceivedText(id int64) error {
	_, err := s.db.Exec(`DELETE FROM received_texts WHERE id = ?`, id)
	return err
}
