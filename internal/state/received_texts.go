package state

import (
	"time"

	"github.com/pocketbase/pocketbase/core"
)

const maxReceivedTexts = 100

type ReceivedText struct {
	ID     string
	Sender string
	Body   string
	At     time.Time
}

// AddReceivedText stores a text and prunes older records atomically.
func (s *Store) AddReceivedText(sender, body string, at time.Time) (string, error) {
	var id string
	err := s.app.RunInTransaction(func(tx core.App) error {
		r := core.NewRecord(mustCollection(tx, "received_texts"))
		r.Set("sender", sender)
		r.Set("body", body)
		r.Set("received", at)
		if err := tx.Save(r); err != nil {
			return err
		}
		id = r.Id
		old, err := tx.FindRecordsByFilter("received_texts", "", "-received", 0, 0)
		if err != nil {
			return err
		}
		if len(old) > maxReceivedTexts {
			for _, item := range old[maxReceivedTexts:] {
				if err := tx.Delete(item); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return id, err
}

// ReceivedTexts returns saved texts newest first.
func (s *Store) ReceivedTexts() ([]ReceivedText, error) {
	recs, err := s.app.FindRecordsByFilter("received_texts", "", "-received", maxReceivedTexts, 0)
	if err != nil {
		return nil, err
	}
	out := make([]ReceivedText, 0, len(recs))
	for _, r := range recs {
		out = append(out, ReceivedText{ID: r.Id, Sender: r.GetString("sender"), Body: r.GetString("body"), At: r.GetDateTime("received").Time()})
	}
	return out, nil
}

// DeleteReceivedText removes a saved text; an already-pruned ID is harmless.
func (s *Store) DeleteReceivedText(id string) error {
	r, err := s.app.FindRecordById("received_texts", id)
	if recordMissing(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.app.Delete(r)
}
