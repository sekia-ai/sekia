package workflow

import (
	"reflect"
	"testing"

	lua "github.com/yuin/gopher-lua"

	"github.com/sekia-ai/sekia/pkg/protocol"
)

func TestGoToLua(t *testing.T) {
	L := lua.NewState()
	defer L.Close()

	tests := []struct {
		name string
		in   any
		want lua.LValue
	}{
		{"nil", nil, lua.LNil},
		{"string", "hello", lua.LString("hello")},
		{"float64", 3.14, lua.LNumber(3.14)},
		{"int", 42, lua.LNumber(42)},
		{"int64", int64(99), lua.LNumber(99)},
		{"bool_true", true, lua.LTrue},
		{"bool_false", false, lua.LFalse},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GoToLua(L, tt.in)
			if got != tt.want {
				t.Errorf("GoToLua(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestLuaToGo(t *testing.T) {
	tests := []struct {
		name string
		in   lua.LValue
		want any
	}{
		{"nil", lua.LNil, nil},
		{"string", lua.LString("hello"), "hello"},
		{"number", lua.LNumber(42), float64(42)},
		{"bool", lua.LTrue, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LuaToGo(tt.in)
			if got != tt.want {
				t.Errorf("LuaToGo(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestMapRoundTrip(t *testing.T) {
	L := lua.NewState()
	defer L.Close()

	original := map[string]any{
		"name":   "test",
		"count":  float64(42),
		"active": true,
		"nested": map[string]any{
			"key": "value",
		},
	}

	tbl := MapToTable(L, original)
	result := TableToMap(tbl)

	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result)
	}
	if !reflect.DeepEqual(m, original) {
		t.Errorf("round-trip mismatch:\n  got:  %v\n  want: %v", m, original)
	}
}

func TestSliceRoundTrip(t *testing.T) {
	L := lua.NewState()
	defer L.Close()

	original := []any{"a", float64(2), true}

	tbl := SliceToTable(L, original)
	result := TableToMap(tbl)

	arr, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", result)
	}
	if !reflect.DeepEqual(arr, original) {
		t.Errorf("round-trip mismatch:\n  got:  %v\n  want: %v", arr, original)
	}
}

func TestEmptyMap(t *testing.T) {
	L := lua.NewState()
	defer L.Close()

	tbl := MapToTable(L, map[string]any{})
	result := TableToMap(tbl)

	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result)
	}
	if len(m) != 0 {
		t.Errorf("expected empty map, got %v", m)
	}
}

func TestEventToLua(t *testing.T) {
	L := lua.NewState()
	defer L.Close()

	ev := protocol.Event{
		ID:        "evt_123",
		Type:      "issue.opened",
		Source:    "github-agent",
		Timestamp: 1700000000,
		Payload: map[string]any{
			"number": float64(42),
			"title":  "Fix bug",
		},
	}

	tbl := EventToLua(L, ev)

	if s := L.GetField(tbl, "id"); s.String() != "evt_123" {
		t.Errorf("id = %s, want evt_123", s)
	}
	if s := L.GetField(tbl, "type"); s.String() != "issue.opened" {
		t.Errorf("type = %s, want issue.opened", s)
	}
	if s := L.GetField(tbl, "source"); s.String() != "github-agent" {
		t.Errorf("source = %s, want github-agent", s)
	}
	if n, ok := L.GetField(tbl, "timestamp").(lua.LNumber); !ok || float64(n) != 1700000000 {
		t.Errorf("timestamp = %v, want 1700000000", L.GetField(tbl, "timestamp"))
	}

	payload := L.GetField(tbl, "payload").(*lua.LTable)
	if n, ok := L.GetField(payload, "number").(lua.LNumber); !ok || float64(n) != 42 {
		t.Errorf("payload.number = %v, want 42", L.GetField(payload, "number"))
	}
	if s := L.GetField(payload, "title"); s.String() != "Fix bug" {
		t.Errorf("payload.title = %s, want Fix bug", s)
	}
}
