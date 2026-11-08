package agent

// Chat sessions live in ADK's in-memory session service, which never forgets
// on its own. The registry keeps it bounded for a long-running server and
// serializes the turns of each conversation.

import (
	"errors"
	"regexp"
	"sort"
	"sync"
	"time"
)

const (
	// sessionIdleTTL is how long an untouched conversation keeps its
	// history before it is dropped.
	sessionIdleTTL = 2 * time.Hour
	// maxSessions caps the conversations held at once; past it the least
	// recently used idle ones go first.
	maxSessions = 500
	// maxSessionTurns caps the messages of one conversation. Every turn
	// adds its tool results to the history the model re-reads, so an endless
	// chat ends up past the context window; past the cap the user starts a
	// new one.
	maxSessionTurns = 60
)

// sessionIDPattern is what a client-supplied session id may look like. The
// server mints 32 hex characters; anything else is refused before it can
// reach the logs or the session store.
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// errSessionBusy is returned when a conversation is already answering: two
// concurrent turns would interleave their events in one history.
var errSessionBusy = errors.New("this conversation is already answering a message")

// errSessionTooLong is returned when a conversation reached maxSessionTurns.
var errSessionTooLong = errors.New("this conversation is too long: start a new chat")

type sessionEntry struct {
	lastUsed time.Time
	busy     bool
	turns    int
}

// sessionRegistry tracks the live conversations.
type sessionRegistry struct {
	mu      sync.Mutex
	entries map[string]*sessionEntry
	ttl     time.Duration
	max     int
	maxTurn int
	now     func() time.Time
	// onEvict runs under the registry lock for every conversation dropped,
	// so its history cannot be deleted after the same id was re-acquired.
	onEvict func(id string)
}

func newSessionRegistry(ttl time.Duration, max int) *sessionRegistry {
	return &sessionRegistry{entries: map[string]*sessionEntry{}, ttl: ttl, max: max, maxTurn: maxSessionTurns, now: time.Now}
}

// acquire claims a conversation for one turn. It fails with errSessionBusy
// while another turn of it runs and with errSessionTooLong once it used its
// turns. known says whether the registry already
// held it (false for a new one, or one that expired or did not survive a
// restart). evicted lists the conversations dropped for idleness or to stay
// under the cap: the caller deletes their history. release must be called
// when the turn ends; calling it more than once is harmless.
func (r *sessionRegistry) acquire(id string) (release func(), known bool, evicted []string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	entry, known := r.entries[id]
	if known && entry.busy {
		return nil, true, nil, errSessionBusy
	}
	if known && r.maxTurn > 0 && entry.turns >= r.maxTurn {
		return nil, true, nil, errSessionTooLong
	}
	for key, e := range r.entries {
		if key != id && !e.busy && now.Sub(e.lastUsed) > r.ttl {
			r.evict(key)
			evicted = append(evicted, key)
		}
	}
	if !known {
		entry = &sessionEntry{}
		r.entries[id] = entry
	}
	entry.busy, entry.lastUsed = true, now
	entry.turns++

	for len(r.entries) > r.max {
		oldest := ""
		for key, e := range r.entries {
			if e.busy {
				continue
			}
			if oldest == "" || e.lastUsed.Before(r.entries[oldest].lastUsed) {
				oldest = key
			}
		}
		if oldest == "" {
			break // every conversation is mid-turn: let the cap breathe
		}
		r.evict(oldest)
		evicted = append(evicted, oldest)
	}
	sort.Strings(evicted)

	var once sync.Once
	release = func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			entry.busy, entry.lastUsed = false, r.now()
		})
	}
	return release, known, evicted, nil
}

// size is the number of conversations held.
func (r *sessionRegistry) size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

// evict forgets a conversation. The caller holds the lock.
func (r *sessionRegistry) evict(id string) {
	delete(r.entries, id)
	if r.onEvict != nil {
		r.onEvict(id)
	}
}
