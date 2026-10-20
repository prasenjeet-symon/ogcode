package session

import (
	"errors"
	"testing"
)

// TestForeignKeyFailuresMeanSessionDeleted pins the contract the agent loop
// relies on: once the session row is gone, inserting a message or part fails on
// its foreign key and surfaces as ErrSessionDeleted instead of a bare SQLite
// constraint error. That is what lets the loop stop at once rather than stream
// a turn whose every write fails.
func TestForeignKeyFailuresMeanSessionDeleted(t *testing.T) {
	store, sessionID := partsTestStore(t)
	msgID := seedMessage(t, store, sessionID, RoleAssistant, 1)

	if err := store.Delete(sessionID); err != nil {
		t.Fatalf("delete session: %v", err)
	}

	if sess, err := store.Get(sessionID); err != nil || sess != nil {
		t.Fatalf("Get after delete = (%v, %v), want (nil, nil)", sess, err)
	}

	err := store.CreateMessage(&MessageInfo{
		ID: NewMessageID(), SessionID: sessionID, Role: RoleAssistant, CreatedAt: Now(),
	})
	if !errors.Is(err, ErrSessionDeleted) {
		t.Fatalf("CreateMessage after delete = %v, want ErrSessionDeleted", err)
	}

	err = store.CreatePart(&Part{
		ID: NewPartID(), MessageID: msgID, SessionID: sessionID,
		Type: PartText, Data: []byte(`{"text":"x"}`), CreatedAt: Now(), UpdatedAt: Now(),
	})
	if !errors.Is(err, ErrSessionDeleted) {
		t.Fatalf("CreatePart after delete = %v, want ErrSessionDeleted", err)
	}
}

// TestNonForeignKeyErrorsAreNotMarkedDeleted keeps the sentinel honest: a
// constraint failure that is not about a vanished parent must stay what it is,
// so the loop does not report an unrelated write bug as a deleted session.
func TestNonForeignKeyErrorsAreNotMarkedDeleted(t *testing.T) {
	store, sessionID := partsTestStore(t)
	msg := &MessageInfo{
		ID: NewMessageID(), SessionID: sessionID, Role: RoleAssistant, CreatedAt: Now(),
	}
	if err := store.CreateMessage(msg); err != nil {
		t.Fatalf("create message: %v", err)
	}
	err := store.CreateMessage(msg) // duplicate primary key
	if err == nil {
		t.Fatal("duplicate message insert succeeded")
	}
	if errors.Is(err, ErrSessionDeleted) {
		t.Fatalf("duplicate primary key reported as ErrSessionDeleted: %v", err)
	}
}
