package workflow

import (
	"strings"

	"github.com/rs/zerolog"
	lua "github.com/yuin/gopher-lua"
)

// NewSandboxedState creates an LState with only safe libraries loaded.
// Dangerous modules (os, io, debug, package) and functions (dofile, loadfile, load) are omitted.
func NewSandboxedState(name string, logger zerolog.Logger) *lua.LState {
	L := lua.NewState(lua.Options{
		SkipOpenLibs: true,
	})

	// Open only safe standard libraries.
	for _, lib := range []struct {
		name string
		fn   lua.LGFunction
	}{
		{lua.BaseLibName, lua.OpenBase},
		{lua.TabLibName, lua.OpenTable},
		{lua.StringLibName, lua.OpenString},
		{lua.MathLibName, lua.OpenMath},
	} {
		L.Push(L.NewFunction(lib.fn))
		L.Push(lua.LString(lib.name))
		L.Call(1, 0)
	}

	// Remove dangerous base globals.
	for _, name := range []string{"dofile", "loadfile", "load", "loadstring"} {
		L.SetGlobal(name, lua.LNil)
	}

	// Override print to route through zerolog.
	wfLogger := logger.With().Str("workflow", name).Logger()
	L.SetGlobal("print", L.NewFunction(func(L *lua.LState) int {
		n := L.GetTop()
		parts := make([]string, n)
		for i := 1; i <= n; i++ {
			parts[i-1] = L.Get(i).String()
		}
		wfLogger.Info().Msg(strings.Join(parts, "\t"))
		return 0
	}))

	return L
}
