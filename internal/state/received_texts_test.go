package state

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// Texts come back newest first, only the newest 100 are kept, and delete
// removes exactly one.
func TestReceivedTextsNewestFirstPrunedAndDeletable(t *testing.T) {
	store, err := OpenState(filepath.Join(t.TempDir(), "mochi.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range maxReceivedTexts + 5 {
		_, err := store.AddReceivedText("alice", fmt.Sprintf("text %d", i), base.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
	}

	texts, err := store.ReceivedTexts()
	if err != nil {
		t.Fatal(err)
	}
	if len(texts) != maxReceivedTexts {
		t.Fatalf("kept %d texts, want %d", len(texts), maxReceivedTexts)
	}
	if want := fmt.Sprintf("text %d", maxReceivedTexts+4); texts[0].Body != want {
		t.Fatalf("newest first: got %q, want %q", texts[0].Body, want)
	}
	if last := texts[len(texts)-1]; last.Body != "text 5" {
		t.Fatalf("oldest kept %q, want %q", last.Body, "text 5")
	}

	victim := texts[10].ID
	if err := store.DeleteReceivedText(victim); err != nil {
		t.Fatal(err)
	}
	after, err := store.ReceivedTexts()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != maxReceivedTexts-1 {
		t.Fatalf("after delete kept %d texts, want %d", len(after), maxReceivedTexts-1)
	}
	for _, text := range after {
		if text.ID == victim {
			t.Fatal("deleted text is still listed")
		}
	}
}
