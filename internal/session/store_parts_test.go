package session

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

// partsTestStore creates a session alongside a bare store so a test can seed
// messages and parts directly.
func partsTestStore(t *testing.T) (*Store, SessionID) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	store := NewStore(database)
	id := NewSessionID()
	if err := store.Create(&Session{
		ID: id, ProjectID: "/p", Directory: "/p", Title: "t",
		SessionType: "build", CreatedAt: Now(), UpdatedAt: Now(),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return store, id
}

// seedMessage writes a message plus n text parts and returns the message id.
func seedMessage(t *testing.T, store *Store, sessionID SessionID, role MessageRole, n int) MessageID {
	t.Helper()
	msgID := NewMessageID()
	if err := store.CreateMessage(&MessageInfo{
		ID: msgID, SessionID: sessionID, Role: role, CreatedAt: Now(),
	}); err != nil {
		t.Fatalf("create message: %v", err)
	}
	for i := 0; i < n; i++ {
		body, _ := json.Marshal(TextPartData{Text: "part"})
		if err := store.CreatePart(&Part{
			ID: NewPartID(), MessageID: msgID, SessionID: sessionID,
			Type: PartText, Data: body, CreatedAt: Now(), UpdatedAt: Now(),
		}); err != nil {
			t.Fatalf("create part: %v", err)
		}
	}
	return msgID
}

// TestGetPartsForMessagesBatches pins the single-query batch that replaced the
// per-message GetParts loop: every requested message comes back with its own
// parts, in creation order, and a request for several messages costs one query.
func TestGetPartsForMessagesBatches(t *testing.T) {
	store, sessionID := partsTestStore(t)

	a := seedMessage(t, store, sessionID, RoleUser, 2)
	b := seedMessage(t, store, sessionID, RoleAssistant, 3)
	c := seedMessage(t, store, sessionID, RoleUser, 1)

	got, err := store.GetPartsForMessages([]MessageID{a, b, c})
	if err != nil {
		t.Fatalf("GetPartsForMessages: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("map holds %d messages, want 3", len(got))
	}
	for id, want := range map[MessageID]int{a: 2, b: 3, c: 1} {
		if len(got[id]) != want {
			t.Errorf("message %s has %d parts, want %d", id, len(got[id]), want)
		}
	}
	// Ordering within a message must be creation order (ascending id).
	for i := 1; i < len(got[b]); i++ {
		if got[b][i-1].ID >= got[b][i].ID {
			t.Errorf("parts of %s out of order: %s then %s", b, got[b][i-1].ID, got[b][i].ID)
		}
	}
	// A part must carry the message it belongs to, not another's.
	for _, p := range got[b] {
		if p.MessageID != b {
			t.Errorf("part %s claims message %s, want %s", p.ID, p.MessageID, b)
		}
	}
}

// TestGetPartsForMessagesEmpty pins that no ids means no query and an empty
// map, so callers need not special-case an empty message list.
func TestGetPartsForMessagesEmpty(t *testing.T) {
	store, _ := partsTestStore(t)
	got, err := store.GetPartsForMessages(nil)
	if err != nil {
		t.Fatalf("GetPartsForMessages(nil): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("map holds %d entries, want 0", len(got))
	}
}

// TestGetMessagesBefore pins fetch-on-top pagination: a non-empty cursor
// returns only messages older than it, still oldest-first, and the limit
// selects the newest of those older messages rather than the oldest.
func TestGetMessagesBefore(t *testing.T) {
	store, sessionID := partsTestStore(t)

	ids := make([]MessageID, 5)
	for i := range ids {
		ids[i] = seedMessage(t, store, sessionID, RoleUser, 0)
	}

	// Cursor at ids[3]: only ids[0..2] are older, and the limit keeps the
	// newest two of them (ids[1], ids[2]), re-sorted oldest-first.
	got, err := store.GetMessages(sessionID, ids[3], 2)
	if err != nil {
		t.Fatalf("GetMessages before: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2", len(got))
	}
	if got[0].Info.ID != ids[1] || got[1].Info.ID != ids[2] {
		t.Errorf("got %s, %s; want %s, %s (oldest-first, newest two before the cursor)",
			got[0].Info.ID, got[1].Info.ID, ids[1], ids[2])
	}

	// A window wider than the remaining older messages returns all of them,
	// oldest-first, excluding the cursor and everything after it.
	got, err = store.GetMessages(sessionID, ids[2], 100)
	if err != nil {
		t.Fatalf("GetMessages before wide: %v", err)
	}
	if len(got) != 2 || got[0].Info.ID != ids[0] || got[1].Info.ID != ids[1] {
		t.Errorf("wide window returned %d messages starting %v; want %s then %s",
			len(got), ids, ids[0], ids[1])
	}
}

// TestGetMessagesBatchesParts pins that reading a session's transcript still
// attaches the right parts per message after the batch rewrite.
func TestGetMessagesBatchesParts(t *testing.T) {
	store, sessionID := partsTestStore(t)
	seedMessage(t, store, sessionID, RoleUser, 1)
	seedMessage(t, store, sessionID, RoleAssistant, 4)

	msgs, err := store.GetMessages(sessionID, "", 100)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	// GetMessages returns the transcript oldest-first.
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if len(msgs[0].Parts) != 1 {
		t.Errorf("older message has %d parts, want 1", len(msgs[0].Parts))
	}
	if len(msgs[1].Parts) != 4 {
		t.Errorf("newest message has %d parts, want 4", len(msgs[1].Parts))
	}
}

// TestGetMessagesPageReportsOlder pins the pager's "anything older?" answer,
// including the case a length-based guess gets wrong: a history that is an
// exact multiple of the page size, whose last page is full yet has nothing
// behind it.
func TestGetMessagesPageReportsOlder(t *testing.T) {
	store, sessionID := partsTestStore(t)

	ids := make([]MessageID, 4)
	for i := range ids {
		ids[i] = seedMessage(t, store, sessionID, RoleUser, 0)
	}

	page, hasOlder, err := store.GetMessagesPage(sessionID, "", 2)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(page) != 2 || page[0].Info.ID != ids[2] || page[1].Info.ID != ids[3] || !hasOlder {
		t.Fatalf("first page = %d messages (hasOlder %v), want %s, %s with older ones behind", len(page), hasOlder, ids[2], ids[3])
	}

	page, hasOlder, err = store.GetMessagesPage(sessionID, page[0].Info.ID, 2)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(page) != 2 || page[0].Info.ID != ids[0] || page[1].Info.ID != ids[1] {
		t.Fatalf("second page = %d messages, want %s, %s", len(page), ids[0], ids[1])
	}
	if hasOlder {
		t.Errorf("a full last page reported older messages; the history is exhausted")
	}

	page, hasOlder, err = store.GetMessagesPage(sessionID, ids[0], 2)
	if err != nil {
		t.Fatalf("past the start: %v", err)
	}
	if len(page) != 0 || hasOlder {
		t.Errorf("past the start = %d messages (hasOlder %v), want none", len(page), hasOlder)
	}
}

// TestGetMessagesEmptyPartsAreAList pins that a message with no parts — an
// errored or aborted turn — is sent as "parts": [] rather than null. The web
// client indexes parts directly, and a null on an older page crashed it.
func TestGetMessagesEmptyPartsAreAList(t *testing.T) {
	store, sessionID := partsTestStore(t)
	seedMessage(t, store, sessionID, RoleAssistant, 0)

	msgs, err := store.GetMessages(sessionID, "", 10)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Parts == nil {
		t.Fatalf("got %d messages with parts %v, want one message with a non-nil empty list", len(msgs), msgs[0].Parts)
	}
	raw, err := json.Marshal(msgs[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"parts":[]`) {
		t.Errorf("encoded message = %s, want \"parts\":[]", raw)
	}
}
