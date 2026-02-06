package workflow

import (
	lua "github.com/yuin/gopher-lua"

	"github.com/sekia-ai/sekia/pkg/protocol"
)

// GoToLua converts an arbitrary Go value to an LValue.
func GoToLua(L *lua.LState, val any) lua.LValue {
	if val == nil {
		return lua.LNil
	}
	switch v := val.(type) {
	case string:
		return lua.LString(v)
	case float64:
		return lua.LNumber(v)
	case float32:
		return lua.LNumber(float64(v))
	case int:
		return lua.LNumber(float64(v))
	case int64:
		return lua.LNumber(float64(v))
	case bool:
		return lua.LBool(v)
	case map[string]any:
		return MapToTable(L, v)
	case []any:
		return SliceToTable(L, v)
	default:
		return lua.LNil
	}
}

// LuaToGo converts an LValue to a Go value.
func LuaToGo(val lua.LValue) any {
	switch v := val.(type) {
	case *lua.LNilType:
		return nil
	case lua.LBool:
		return bool(v)
	case lua.LNumber:
		return float64(v)
	case lua.LString:
		return string(v)
	case *lua.LTable:
		return TableToMap(v)
	default:
		return nil
	}
}

// MapToTable converts a map[string]any to an LTable.
func MapToTable(L *lua.LState, m map[string]any) *lua.LTable {
	tbl := L.NewTable()
	for k, v := range m {
		L.SetField(tbl, k, GoToLua(L, v))
	}
	return tbl
}

// SliceToTable converts a []any to an LTable with 1-based integer keys.
func SliceToTable(L *lua.LState, s []any) *lua.LTable {
	tbl := L.NewTable()
	for _, v := range s {
		tbl.Append(GoToLua(L, v))
	}
	return tbl
}

// TableToMap converts an LTable to map[string]any.
// Tables with only sequential integer keys (1..n) are returned as []any.
func TableToMap(tbl *lua.LTable) any {
	// Check if this is an array (only sequential integer keys starting at 1).
	maxN := tbl.MaxN()
	isArray := maxN > 0
	if isArray {
		count := 0
		tbl.ForEach(func(k, v lua.LValue) {
			count++
		})
		isArray = count == maxN
	}

	if isArray {
		arr := make([]any, 0, maxN)
		for i := 1; i <= maxN; i++ {
			arr = append(arr, LuaToGo(tbl.RawGetInt(i)))
		}
		return arr
	}

	m := make(map[string]any)
	tbl.ForEach(func(k, v lua.LValue) {
		if ks, ok := k.(lua.LString); ok {
			m[string(ks)] = LuaToGo(v)
		}
	})
	return m
}

// EventToLua converts a protocol.Event to a Lua table.
func EventToLua(L *lua.LState, ev protocol.Event) *lua.LTable {
	tbl := L.NewTable()
	L.SetField(tbl, "id", lua.LString(ev.ID))
	L.SetField(tbl, "type", lua.LString(ev.Type))
	L.SetField(tbl, "source", lua.LString(ev.Source))
	L.SetField(tbl, "timestamp", lua.LNumber(ev.Timestamp))
	L.SetField(tbl, "payload", MapToTable(L, ev.Payload))
	return tbl
}
