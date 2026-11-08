package agent

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// clockedRegistry is a registry whose clock the test moves by hand.
func clockedRegistry(ttl time.Duration, max int) (*sessionRegistry, *time.Time) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	r := newSessionRegistry(ttl, max)
	r.now = func() time.Time { return now }
	return r, &now
}

func mustAcquire(t *testing.T, r *sessionRegistry, id string) (release func(), known bool, evicted []string) {
	t.Helper()
	release, known, evicted, err := r.acquire(id)
	if err != nil {
		t.Fatalf("acquire(%q): %v", id, err)
	}
	return release, known, evicted
}

func TestSessionAcquireNewThenKnown(t *testing.T) {
	r, _ := clockedRegistry(time.Hour, 10)
	release, known, _ := mustAcquire(t, r, "session-1")
	if known {
		t.Errorf("first acquire reported the session as known")
	}
	release()
	release, known, _ = mustAcquire(t, r, "session-1")
	defer release()
	if !known {
		t.Errorf("second acquire did not recognize the session")
	}
	if r.size() != 1 {
		t.Errorf("size = %d, want 1", r.size())
	}
}

func TestSessionBusyRefusesASecondTurn(t *testing.T) {
	r, _ := clockedRegistry(time.Hour, 10)
	release, _, _ := mustAcquire(t, r, "session-1")

	if _, _, _, err := r.acquire("session-1"); !errors.Is(err, errSessionBusy) {
		t.Fatalf("concurrent acquire err = %v, want errSessionBusy", err)
	}
	if _, _, _, err := r.acquire("session-2"); err != nil {
		t.Errorf("another conversation must not be blocked: %v", err)
	}

	release()
	if _, _, _, err := r.acquire("session-1"); err != nil {
		t.Errorf("acquire after release: %v", err)
	}
}

func TestSessionReleaseIsIdempotent(t *testing.T) {
	r, now := clockedRegistry(time.Hour, 10)
	release, _, _ := mustAcquire(t, r, "session-1")
	release()
	// A second release must neither panic nor free a later turn's claim.
	second, _, _ := mustAcquire(t, r, "session-1")
	*now = now.Add(time.Minute)
	release()
	if _, _, _, err := r.acquire("session-1"); !errors.Is(err, errSessionBusy) {
		t.Errorf("a stale release freed the running turn: err = %v", err)
	}
	second()
}

func TestSessionIdleConversationsExpire(t *testing.T) {
	r, now := clockedRegistry(time.Hour, 10)
	for _, id := range []string{"session-a", "session-b"} {
		release, _, _ := mustAcquire(t, r, id)
		release()
	}
	*now = now.Add(30 * time.Minute)
	release, _, _ := mustAcquire(t, r, "session-b") // refreshed
	release()

	*now = now.Add(45 * time.Minute) // a is 75m idle, b is 45m
	var dropped []string
	r.onEvict = func(id string) { dropped = append(dropped, id) }
	release, known, evicted := mustAcquire(t, r, "session-c")
	defer release()

	if known {
		t.Errorf("a new session reported as known")
	}
	if len(evicted) != 1 || evicted[0] != "session-a" || len(dropped) != 1 || dropped[0] != "session-a" {
		t.Errorf("evicted = %v, dropped = %v, want only session-a", evicted, dropped)
	}
	if r.size() != 2 {
		t.Errorf("size = %d, want 2 (b and c)", r.size())
	}
}

func TestSessionExpiredIdComesBackUnknown(t *testing.T) {
	r, now := clockedRegistry(time.Hour, 10)
	release, _, _ := mustAcquire(t, r, "session-a")
	release()
	*now = now.Add(2 * time.Hour)
	// The expired conversation is acquired again by its owner: it is new.
	// (The expiry sweep skips the id being acquired, so it is not "known"
	// only if it was evicted by someone else first.)
	other, _, evicted := mustAcquire(t, r, "session-b")
	defer other()
	if len(evicted) != 1 || evicted[0] != "session-a" {
		t.Fatalf("evicted = %v, want session-a", evicted)
	}
	release, known, _ := mustAcquire(t, r, "session-a")
	defer release()
	if known {
		t.Errorf("an evicted session must come back unknown")
	}
}

func TestSessionCapEvictsLeastRecentlyUsedIdle(t *testing.T) {
	r, now := clockedRegistry(time.Hour, 3)
	for _, id := range []string{"session-1", "session-2", "session-3"} {
		release, _, _ := mustAcquire(t, r, id)
		release()
		*now = now.Add(time.Minute)
	}
	// Touch session-1: session-2 becomes the least recently used.
	release, _, _ := mustAcquire(t, r, "session-1")
	release()
	*now = now.Add(time.Minute)

	release, _, evicted := mustAcquire(t, r, "session-4")
	defer release()
	if len(evicted) != 1 || evicted[0] != "session-2" {
		t.Errorf("evicted = %v, want [session-2]", evicted)
	}
	if r.size() != 3 {
		t.Errorf("size = %d, want the cap (3)", r.size())
	}
}

func TestSessionCapNeverEvictsABusyConversation(t *testing.T) {
	r, _ := clockedRegistry(time.Hour, 2)
	r1, _, _ := mustAcquire(t, r, "session-1")
	r2, _, _ := mustAcquire(t, r, "session-2")
	// Both mid-turn: the cap breathes instead of killing a running chat.
	r3, _, evicted := mustAcquire(t, r, "session-3")
	if len(evicted) != 0 || r.size() != 3 {
		t.Errorf("evicted = %v, size = %d: a busy conversation was dropped", evicted, r.size())
	}
	r1()
	r2()
	r3()
	// Once idle, the next acquire brings it back under the cap.
	release, _, evicted := mustAcquire(t, r, "session-4")
	defer release()
	if r.size() != 2 || len(evicted) != 2 {
		t.Errorf("size = %d, evicted = %v, want the cap restored", r.size(), evicted)
	}
}

func TestSessionTurnCap(t *testing.T) {
	r, _ := clockedRegistry(time.Hour, 10)
	r.maxTurn = 3
	for range 3 {
		release, _, _ := mustAcquire(t, r, "session-1")
		release()
	}
	if _, _, _, err := r.acquire("session-1"); !errors.Is(err, errSessionTooLong) {
		t.Errorf("fourth turn err = %v, want errSessionTooLong", err)
	}
	if _, _, _, err := r.acquire("session-2"); err != nil {
		t.Errorf("another conversation must start: %v", err)
	}
}

// Hammered from many goroutines the registry must keep one turn per
// conversation, and its size within the cap plus the running turns.
func TestSessionRegistryConcurrentUse(t *testing.T) {
	r := newSessionRegistry(time.Hour, 20)
	r.maxTurn = 0
	var running sync.Map
	var wg sync.WaitGroup
	for g := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				id := fmt.Sprintf("session-%d", (g+i)%30)
				release, _, _, err := r.acquire(id)
				if err != nil {
					continue
				}
				if _, loaded := running.LoadOrStore(id, true); loaded {
					t.Errorf("two turns of %s ran at once", id)
				}
				running.Delete(id)
				release()
			}
		}()
	}
	wg.Wait()
	if size := r.size(); size > 20 {
		t.Errorf("size = %d after the storm, want <= 20", size)
	}
}

func TestSessionIDPattern(t *testing.T) {
	valid := []string{"0123456789abcdef0123456789abcdef", "abcdefgh", "A_b-C_d-1234", string(make([]byte, 0)) + "a1234567"}
	for _, id := range valid {
		if !sessionIDPattern.MatchString(id) {
			t.Errorf("%q refused, want accepted", id)
		}
	}
	long := make([]byte, 65)
	for i := range long {
		long[i] = 'a'
	}
	invalid := []string{"", "short", string(long), "has space1", "new\nline12", "../../etc/passwd", "emoji-😀-12345", "quote\"12345", "a1234567\n"}
	for _, id := range invalid {
		if sessionIDPattern.MatchString(id) {
			t.Errorf("%q accepted, want refused", id)
		}
	}
}
