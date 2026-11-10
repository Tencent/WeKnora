package chatpipeline

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// Define a test Plugin implementation
type testPlugin struct {
	name          string
	events        []types.EventType
	shouldError   bool
	errorToReturn *PluginError
}

func (p *testPlugin) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	if p.shouldError {
		return p.errorToReturn
	}
	fmt.Printf("Plugin %s triggered\n", p.name)
	err := next()
	fmt.Printf("Plugin %s finished\n", p.name)
	return err
}

func (p *testPlugin) ActivationEvents() []types.EventType {
	return p.events
}

func TestTrigger(t *testing.T) {
	// Prepare test data
	ctx := context.Background()
	chatManage := &types.ChatManage{}
	testEvent := types.EventType("test_event")

	// Test scenario 1: No plugins registered
	t.Run("NoPluginsRegistered", func(t *testing.T) {
		manager := &EventManager{}
		err := manager.Trigger(ctx, testEvent, chatManage)
		if err != nil {
			t.Errorf("Expected nil error, got %v", err)
		}
	})

	// Test scenario 2: Register a normally working plugin
	t.Run("SinglePluginSuccess", func(t *testing.T) {
		manager := &EventManager{}
		plugin := &testPlugin{
			name:   "test_plugin",
			events: []types.EventType{testEvent},
		}
		manager.Register(plugin)

		err := manager.Trigger(ctx, testEvent, chatManage)
		if err != nil {
			t.Errorf("Expected nil error, got %v", err)
		}
	})

	// Test scenario 3: Plugin chain call
	t.Run("PluginChain", func(t *testing.T) {
		manager := &EventManager{}
		plugin1 := &testPlugin{
			name:   "plugin1",
			events: []types.EventType{testEvent},
		}
		plugin2 := &testPlugin{
			name:   "plugin2",
			events: []types.EventType{testEvent},
		}
		manager.Register(plugin1)
		manager.Register(plugin2)

		err := manager.Trigger(ctx, testEvent, chatManage)
		if err != nil {
			t.Errorf("Expected nil error, got %v", err)
		}
	})

	// Test scenario 4: Plugin returns error
	t.Run("PluginReturnsError", func(t *testing.T) {
		manager := &EventManager{}
		expectedErr := &PluginError{Description: "test error"}
		plugin := &testPlugin{
			name:          "error_plugin",
			events:        []types.EventType{testEvent},
			shouldError:   true,
			errorToReturn: expectedErr,
		}
		manager.Register(plugin)

		err := manager.Trigger(ctx, testEvent, chatManage)
		if err != expectedErr {
			t.Errorf("Expected error %v, got %v", expectedErr, err)
		}
	})

	// Test scenario 5: A plugin in the chain returns error
	t.Run("ErrorInPluginChain", func(t *testing.T) {
		manager := &EventManager{}
		expectedErr := &PluginError{Description: "test error"}
		plugin1 := &testPlugin{
			name:   "plugin1",
			events: []types.EventType{testEvent},
		}
		plugin2 := &testPlugin{
			name:          "plugin2",
			events:        []types.EventType{testEvent},
			shouldError:   true,
			errorToReturn: expectedErr,
		}
		manager.Register(plugin1)
		manager.Register(plugin2)

		err := manager.Trigger(ctx, testEvent, chatManage)
		if err != expectedErr {
			t.Errorf("Expected error %v, got %v", expectedErr, err)
		}
	})
}
