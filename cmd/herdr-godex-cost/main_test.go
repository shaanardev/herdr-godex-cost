package main

import (
	"context"
	"testing"
)

func TestInvalidCommands(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", "")
	for _, args := range [][]string{{"refresh"}, {"watch", "-pane", "p", "-interval", "0"}, {"bogus"}, {"refresh", "-pane", "p", "extra"}} {
		if err := run(context.Background(), args); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}

func TestEventWithoutPane(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_EVENT_JSON", "{}")
	if err := run(context.Background(), []string{"event"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PLUGIN_EVENT_JSON", "broken")
	if err := run(context.Background(), []string{"event"}); err == nil {
		t.Fatal("accepted broken event")
	}
}
