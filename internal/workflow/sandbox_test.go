package workflow

import (
	"os"
	"testing"

	"github.com/rs/zerolog"
	lua "github.com/yuin/gopher-lua"
)

func testLogger() zerolog.Logger {
	return zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).
		With().Timestamp().Logger()
}

func TestSandbox_SafeLibsAvailable(t *testing.T) {
	L := NewSandboxedState("test", testLogger())
	defer L.Close()

	// string library works.
	if err := L.DoString(`assert(string.find("hello world", "world") == 7)`); err != nil {
		t.Errorf("string lib: %v", err)
	}

	// table library works.
	if err := L.DoString(`local t = {1,2,3}; table.insert(t, 4); assert(#t == 4)`); err != nil {
		t.Errorf("table lib: %v", err)
	}

	// math library works.
	if err := L.DoString(`assert(math.floor(3.7) == 3)`); err != nil {
		t.Errorf("math lib: %v", err)
	}

	// base functions work.
	if err := L.DoString(`assert(type("hello") == "string")`); err != nil {
		t.Errorf("base type(): %v", err)
	}
	if err := L.DoString(`assert(tostring(42) == "42")`); err != nil {
		t.Errorf("base tostring(): %v", err)
	}
	if err := L.DoString(`assert(tonumber("42") == 42)`); err != nil {
		t.Errorf("base tonumber(): %v", err)
	}
}

func TestSandbox_DangerousLibsBlocked(t *testing.T) {
	L := NewSandboxedState("test", testLogger())
	defer L.Close()

	tests := []struct {
		name string
		code string
	}{
		{"os.execute", `os.execute("echo hi")`},
		{"io.open", `io.open("/etc/passwd")`},
		{"debug.getinfo", `debug.getinfo(1)`},
		{"dofile", `dofile("some_file.lua")`},
		{"loadfile", `loadfile("some_file.lua")`},
		{"load", `load("return 1")`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := L.DoString(tt.code); err == nil {
				t.Errorf("expected error for %s, got nil", tt.name)
			}
		})
	}
}

func TestSandbox_PrintRedirected(t *testing.T) {
	L := NewSandboxedState("test", testLogger())
	defer L.Close()

	// print should not error (it's overridden, not removed).
	if err := L.DoString(`print("hello from sandbox")`); err != nil {
		t.Errorf("print: %v", err)
	}

	// Verify it's a function, not nil.
	fn := L.GetGlobal("print")
	if fn.Type() != lua.LTFunction {
		t.Errorf("print type = %s, want function", fn.Type())
	}
}
