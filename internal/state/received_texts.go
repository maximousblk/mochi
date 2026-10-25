package state

import (
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// maxReceivedTexts is how many received texts are kept. The oldest are dropped
// as new ones arrive, so the inbox cannot grow without bound.
const maxReceivedTexts = 100

// newestFirst orders texts by when they arrived, then by when they were saved.
const newestFirst = "-received,-@rowid"

// ReceivedText is a text that arrived on the Send text port.
type ReceivedText struct {
	ID     string
	Sender string
	Body   string
	At     time.Time
}

// AddReceivedText saves a received text and drops all but the newest
// maxReceivedTexts, in one transaction. It returns the new text's ID.
func (s *Store) AddReceivedText(sender, body string, at time.Time) (string, error) {
	var id string
	err := s.app.RunInTransaction(func(tx core.App) error {
		r := core.NewRecord(s.collection("received_texts"))
		r.Set("sender", sender)
		r.Set("body", body)
		r.Set("received", at)
		if err := tx.Save(r); err != nil {
			return err
		}
		id = r.Id
		old, err := tx.FindRecordsByFilter("received_texts", "", newestFirst, 0, maxReceivedTexts)
		if err != nil {
			return err
		}
		for _, r := range old {
			if err := tx.Delete(r); err != nil {
				return err
			}
		}
		return nil
	})
	return id, err
}

// ReceivedTexts returns the saved texts, newest first.
func (s *Store) ReceivedTexts() ([]ReceivedText, error) {
	recs, err := s.app.FindRecordsByFilter("received_texts", "", newestFirst, maxReceivedTexts, 0)
	if err != nil {
		return nil, err
	}
	out := make([]ReceivedText, 0, len(recs))
	for _, r := range recs {
		out = append(out, ReceivedText{ID: r.Id, Sender: r.GetString("sender"), Body: r.GetString("body"), At: r.GetDateTime("received").Time()})
	}
	return out, nil
}

// DeleteReceivedText removes one saved text. Deleting an ID that is gone is not
// an error.
func (s *Store) DeleteReceivedText(id string) error {
	r, err := s.app.FindRecordById("received_texts", id)
	if missing(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.app.Delete(r)
}
