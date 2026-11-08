package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func marshaledLen(t *testing.T, v any) int {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return len(raw)
}

func numbered(n int) []any {
	items := make([]any, n)
	for i := range items {
		items[i] = map[string]any{"id": i, "name": "item-" + strings.Repeat("x", 20)}
	}
	return items
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello", 3, "hel…"},
		{"hello", 0, "…"},
		{"hello", -4, "…"},
		{"", 3, ""},
		{"ééé", 3, "é…"}, // 3 bytes would split the second é: step back to a boundary
		{"ééé", 4, "éé…"},
		{"日本語", 4, "日…"},
	}
	for _, c := range cases {
		got := truncate(c.in, c.max)
		if got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("truncate(%q, %d) = %q is not valid UTF-8", c.in, c.max, got)
		}
	}
}

func TestPreviewIsUTF8Safe(t *testing.T) {
	message := strings.Repeat("é", 100)
	got := preview(message)
	if !utf8.ValidString(got) {
		t.Errorf("preview split a rune: %q", got)
	}
	if preview("a\nb") != "a b" {
		t.Errorf("preview kept a newline")
	}
}

func TestBoundResultNilAndEmpty(t *testing.T) {
	boundResult(nil, 100)
	empty := map[string]any{}
	boundResult(empty, 100)
	if len(empty) != 0 {
		t.Errorf("empty result changed: %v", empty)
	}
}

func TestBoundResultKeepsAFittingResultUntouched(t *testing.T) {
	result := map[string]any{"findings": numbered(3), "total": 3}
	before, _ := json.Marshal(result)
	boundResult(result, 10_000)
	after, _ := json.Marshal(result)
	if string(before) != string(after) {
		t.Errorf("a fitting result was rewritten:\n%s\n%s", before, after)
	}
	if _, ok := result["truncated"]; ok {
		t.Errorf("a fitting result got a truncation marker")
	}
}

func TestBoundResultTrimsAListOneLevelDown(t *testing.T) {
	result := map[string]any{"data": map[string]any{"items": numbered(300), "count": 300}}
	boundResult(result, 3000)

	if n := marshaledLen(t, result); n > 3000 {
		t.Errorf("result is %dB, want <= 3000", n)
	}
	data := result["data"].(map[string]any)
	kept := data["items"].([]any)
	dropped := result["truncated_items"].(int)
	if len(kept) == 0 || dropped == 0 || len(kept)+dropped != 300 {
		t.Errorf("kept %d + dropped %d, want 300 with both > 0", len(kept), dropped)
	}
	if data["count"] != float64(300) {
		t.Errorf("sibling field lost: %v", data["count"])
	}
	if result["truncated"] != true || result["truncation_note"] != truncationNote {
		t.Errorf("markers missing: %v", result)
	}
	// Items are cut from the end: the head of the list survives, in order.
	for i, item := range kept {
		if item.(map[string]any)["id"] != float64(i) {
			t.Fatalf("item %d is %v: the list is not a prefix", i, item)
		}
	}
}

// MCP tools answer {"output":"<json text>"}: the escaping would eat the
// budget and hide the list, so the text is decoded first.
func TestBoundResultDecodesJSONStrings(t *testing.T) {
	raw, _ := json.Marshal(numbered(300))
	result := map[string]any{"output": string(raw)}
	boundResult(result, 3000)

	if n := marshaledLen(t, result); n > 3000 {
		t.Errorf("result is %dB, want <= 3000", n)
	}
	list, ok := result["output"].([]any)
	if !ok || len(list) == 0 {
		t.Fatalf("output was not decoded into a list: %T", result["output"])
	}
	if result["truncated_items"] == nil {
		t.Errorf("no truncated_items marker: %v", result)
	}
}

func TestBoundResultDecodesJSONStringsThatThenFit(t *testing.T) {
	// Escaped, the quotes double the size and break the limit; decoded it
	// fits, so nothing is cut.
	inner := map[string]any{}
	for i := range 40 {
		inner[strings.Repeat("k", 3)+string(rune('a'+i%26))+string(rune('A'+i/26))] = i
	}
	raw, _ := json.Marshal(inner)
	result := map[string]any{"output": string(raw)}
	limit := marshaledLen(t, map[string]any{"output": inner}) + 20
	if marshaledLen(t, result) <= limit {
		t.Fatalf("fixture: escaped %dB does not exceed %dB", marshaledLen(t, result), limit)
	}
	boundResult(result, limit)
	if result["truncated"] != nil {
		t.Errorf("a result that fits once decoded was marked truncated")
	}
	if _, ok := result["output"].(map[string]any); !ok {
		t.Errorf("output not decoded: %T", result["output"])
	}
}

func TestBoundResultLeavesNonJSONStringsAlone(t *testing.T) {
	result := map[string]any{"output": "[not json", "other": "{also not"}
	boundResult(result, 100)
	if result["output"] != "[not json" || result["other"] != "{also not" {
		t.Errorf("malformed JSON text was rewritten: %v", result)
	}
}

func TestBoundResultTrimsOnlyTheLargestList(t *testing.T) {
	small := numbered(3)
	result := map[string]any{"small": small, "big": numbered(400)}
	boundResult(result, 3000)

	if got := result["small"].([]any); len(got) != 3 {
		t.Errorf("the small list lost items: %d left", len(got))
	}
	if got := result["big"].([]any); len(got) >= 400 {
		t.Errorf("the big list was not trimmed")
	}
	if n := marshaledLen(t, result); n > 3000 {
		t.Errorf("result is %dB, want <= 3000", n)
	}
}

func TestBoundResultSingleHugeItemBecomesAPreview(t *testing.T) {
	// One item that does not fit leaves nothing to trim down to.
	result := map[string]any{"items": []any{map[string]any{"blob": strings.Repeat("z", 9000)}}}
	boundResult(result, 2000)
	if n := marshaledLen(t, result); n > 2000 {
		t.Errorf("result is %dB, want <= 2000", n)
	}
	if result["truncated"] != true || result["preview"] == nil || result["truncation_note"] != previewNote {
		t.Errorf("not a noted preview: keys %v", keysOf(result))
	}
}

func TestBoundResultPreviewShrinksForEscaping(t *testing.T) {
	// Every quote costs two bytes once escaped; control characters six.
	for name, text := range map[string]string{
		"quotes":   strings.Repeat(`"`, 6000),
		"controls": strings.Repeat("\x01", 6000),
		"newlines": strings.Repeat("\n", 6000),
	} {
		result := map[string]any{"output": text}
		boundResult(result, 1500)
		if n := marshaledLen(t, result); n > 1500 {
			t.Errorf("%s: result is %dB, want <= 1500", name, n)
		}
		if result["truncated"] != true {
			t.Errorf("%s: not marked truncated", name)
		}
	}
}

func TestBoundResultPreviewNeverSplitsARune(t *testing.T) {
	result := map[string]any{"output": strings.Repeat("日", 5000)}
	boundResult(result, 1000)
	preview, _ := result["preview"].(string)
	if !utf8.ValidString(preview) {
		t.Errorf("preview is not valid UTF-8")
	}
	if n := marshaledLen(t, result); n > 1000 {
		t.Errorf("result is %dB, want <= 1000", n)
	}
}

func TestBoundResultSurvivesTinyBudgets(t *testing.T) {
	for _, limit := range []int{0, 1, 50, markerRoom, markerRoom + 1} {
		result := map[string]any{"output": strings.Repeat("x", 3000), "list": numbered(50)}
		boundResult(result, limit) // must not panic or loop
		if result["truncated"] != true {
			t.Errorf("limit %d: not marked truncated", limit)
		}
	}
}

func TestBoundResultOutputStaysValidJSON(t *testing.T) {
	result := map[string]any{"findings": numbered(500), "total": 500}
	boundResult(result, 2500)
	raw, _ := json.Marshal(result)
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Errorf("bounded result is not valid JSON: %v", err)
	}
}

func TestBoundResultKeepsTypedValuesWhenItFits(t *testing.T) {
	// A typed struct under the limit must come back as it was.
	type finding struct{ ID int }
	result := map[string]any{"f": finding{ID: 7}}
	boundResult(result, 1000)
	if got, ok := result["f"].(finding); !ok || got.ID != 7 {
		t.Errorf("typed value rewritten: %#v", result["f"])
	}
}

func keysOf(m map[string]any) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
