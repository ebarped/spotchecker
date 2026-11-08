package agent

// Bounding of tool results. Every tool result — local or MCP — is what the
// model consumes AND what the session history stores, so an unbounded one
// (a base64 render, a huge JSON dump) inflates every later request of the
// chat until the provider starts answering 500s. boundResult runs on every
// result before the model sees it.

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"
)

// markerRoom is the space kept free for the truncation markers.
const markerRoom = 200

// truncationNote tells the model what to do with a cut result.
const truncationNote = "list cut to fit; narrow the request (filters, offset, a smaller limit) instead of repeating it"

// previewNote tells the model a result was cut mid-way and is not a whole.
const previewNote = "result cut to fit; ask for a narrower slice (a specific field, id or time range) instead of repeating the call"

// truncate bounds s to at most maxBytes bytes plus an ellipsis, never
// splitting a UTF-8 sequence.
func truncate(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	end := max(maxBytes, 0)
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + "…"
}

// boundResult keeps a tool result under maxChars bytes of JSON, in place
// (the callback must hand back the same map the runner gave it):
//
//  1. a result that fits is left untouched;
//  2. string fields holding JSON (what MCP tools return, e.g.
//     {"output":"[...]"}) are decoded, which drops the escaping and lets
//     their lists be trimmed;
//  3. the largest list (top level or one level down) loses items from the
//     end until the result fits, with markers saying how many went — the
//     result stays valid JSON the model can read and narrow from;
//  4. anything else becomes a bounded preview.
func boundResult(result map[string]any, maxChars int) {
	if len(result) == 0 {
		return
	}
	raw, err := json.Marshal(result)
	if err != nil || len(raw) <= maxChars {
		return
	}
	// Work on a plain JSON copy: tool results hold typed structs, and only
	// []any lists can be trimmed generically.
	var plain map[string]any
	if err := json.Unmarshal(raw, &plain); err == nil {
		expandJSONStrings(plain)
		if fits(plain, maxChars) {
			replaceResult(result, plain)
			return
		}
		if dropped := trimLargestList(plain, maxChars-markerRoom); dropped > 0 {
			replaceResult(result, plain)
			result["truncated"] = true
			result["truncated_items"] = dropped
			result["truncation_note"] = truncationNote
			return
		}
	}
	replaceResult(result, boundedPreview(string(raw), maxChars))
}

// boundedPreview wraps the start of text in a preview result that marshals
// under maxChars bytes. JSON escaping can grow the text (quotes, control
// characters), so the cut shrinks until the whole result fits.
func boundedPreview(text string, maxChars int) map[string]any {
	n := max(maxChars-markerRoom, 0)
	for {
		preview := map[string]any{"truncated": true, "preview": truncate(text, n), "truncation_note": previewNote}
		if n == 0 || fits(preview, maxChars) {
			return preview
		}
		n = n * 3 / 4
	}
}

// fits reports whether v marshals to at most limit bytes.
func fits(v any, limit int) bool {
	raw, err := json.Marshal(v)
	return err == nil && len(raw) <= limit
}

// expandJSONStrings decodes the top-level string fields of plain that hold
// a JSON object or array.
func expandJSONStrings(plain map[string]any) {
	for k, v := range plain {
		text, ok := v.(string)
		if !ok {
			continue
		}
		trimmed := strings.TrimSpace(text)
		if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
			continue
		}
		var decoded any
		if json.Unmarshal([]byte(trimmed), &decoded) == nil {
			plain[k] = decoded
		}
	}
}

// listSlot is a list inside the result and how to put a shorter one back.
type listSlot struct {
	items []any
	size  int
	set   func([]any)
}

// listSlots finds the lists of plain: top-level values and the values of
// top-level objects. Ordered by serialized size, largest first.
func listSlots(plain map[string]any) []listSlot {
	var slots []listSlot
	add := func(items []any, set func([]any)) {
		if len(items) < 2 {
			return // nothing to trim down to: one item that does not fit is a preview case
		}
		raw, err := json.Marshal(items)
		if err != nil {
			return
		}
		slots = append(slots, listSlot{items: items, size: len(raw), set: set})
	}
	for k, v := range plain {
		switch value := v.(type) {
		case []any:
			add(value, func(items []any) { plain[k] = items })
		case map[string]any:
			for inner, iv := range value {
				if items, ok := iv.([]any); ok {
					add(items, func(items []any) { value[inner] = items })
				}
			}
		}
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i].size > slots[j].size })
	return slots
}

// trimLargestList shortens the largest list of plain until plain marshals
// under budget bytes, keeping as many items as possible (binary search: the
// size grows with the item count). It returns how many items went; 0 means
// no list could make it fit and plain is left as it was.
func trimLargestList(plain map[string]any, budget int) int {
	slots := listSlots(plain)
	if len(slots) == 0 {
		return 0
	}
	slot := slots[0]
	best := 0
	lo, hi := 1, len(slot.items)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		slot.set(slot.items[:mid])
		if fits(plain, budget) {
			best, lo = mid, mid+1
		} else {
			hi = mid - 1
		}
	}
	if best == 0 {
		slot.set(slot.items)
		return 0
	}
	slot.set(slot.items[:best])
	return len(slot.items) - best
}

// replaceResult swaps the contents of result in place.
func replaceResult(result, with map[string]any) {
	for k := range result {
		delete(result, k)
	}
	for k, v := range with {
		result[k] = v
	}
}
