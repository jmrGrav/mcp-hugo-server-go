package server

import "testing"

func TestDefaultServerCapabilitiesDeclareSharedSurfaces(t *testing.T) {
	caps := defaultServerCapabilities()
	if caps == nil {
		t.Fatal("defaultServerCapabilities() returned nil")
	}
	if caps.Logging == nil {
		t.Fatal("logging capabilities must be declared explicitly")
	}
	if caps.Tools == nil || !caps.Tools.ListChanged {
		t.Fatalf("tool capabilities = %#v want listChanged=true", caps.Tools)
	}
	if caps.Prompts == nil || !caps.Prompts.ListChanged {
		t.Fatalf("prompt capabilities = %#v want listChanged=true", caps.Prompts)
	}
	if caps.Resources == nil || !caps.Resources.ListChanged || !caps.Resources.Subscribe {
		t.Fatalf("resource capabilities = %#v want listChanged=true subscribe=true", caps.Resources)
	}
}
