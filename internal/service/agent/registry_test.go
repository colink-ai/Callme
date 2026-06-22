package agent

import (
	"context"
	"testing"
)

type registryTestAdapter struct{}

func (registryTestAdapter) StartSession(context.Context, string, *SessionRequest) error { return nil }
func (registryTestAdapter) Prompt(context.Context, string, string, []ImageContent, func(Chunk)) error {
	return nil
}
func (registryTestAdapter) StopSession(string) error                     { return nil }
func (registryTestAdapter) GetSessionStatus(string) SessionStatus        { return SessionStatusIdle }
func (registryTestAdapter) CheckHealth(context.Context, AgentSpec) error { return nil }
func (registryTestAdapter) AgentSessionID(string) string                 { return "agent-session" }
func (registryTestAdapter) UsedNativeResume(string) bool                 { return true }

func TestRegistry(t *testing.T) {
	typ := "registry_test"
	RegisterPlugin(PluginMeta{
		Type:        typ,
		Name:        "Registry Test",
		Description: "test plugin",
		DefaultPath: "/bin/test",
		Factory:     func() Adapter { return registryTestAdapter{} },
	})
	if GetAdapter("missing") != nil {
		t.Fatal("missing adapter should be nil")
	}
	if GetAdapter(typ) == nil {
		t.Fatal("registered adapter should be available")
	}
	if DefaultPathFor(typ) != "/bin/test" || DefaultPathFor("missing") != "" {
		t.Fatal("unexpected default path")
	}
	types := GetTypes()
	found := false
	for _, item := range types {
		if item.Type == typ && item.Name == "Registry Test" && item.DefaultPath == "/bin/test" {
			found = true
		}
	}
	if !found {
		t.Fatalf("registered type not found in GetTypes: %+v", types)
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	typ := "registry_duplicate_test"
	RegisterPlugin(PluginMeta{Type: typ, Factory: func() Adapter { return registryTestAdapter{} }})
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate registration should panic")
		}
	}()
	RegisterPlugin(PluginMeta{Type: typ, Factory: func() Adapter { return registryTestAdapter{} }})
}
