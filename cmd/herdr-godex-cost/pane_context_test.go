package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestPaneContextResolution(t *testing.T) {
	for _, test := range []struct {
		name, command, explicit, action, event, want string
		missing                                      bool
	}{
		{name: "explicit pane", command: "refresh", explicit: " pane ", action: "broken", want: "pane"},
		{name: "focused action pane", command: "refresh", action: `{"focused_pane_id":"focused","pane_id":"fallback"}`, want: "focused"},
		{name: "watch action context", command: "watch", action: `{"focused_pane_id":"focused"}`, event: "broken", want: "focused"},
		{name: "action pane fallback", command: "refresh", action: `{"pane_id":"fallback"}`, want: "fallback"},
		{name: "nested action fallback", command: "refresh", action: `{"data":{"pane_id":"nested"}}`, want: "nested"},
		{name: "whitespace is absent", command: "refresh", explicit: " ", action: `{"focused_pane_id":" ","pane_id":" fallback "}`, want: "fallback"},
		{name: "event data takes precedence", command: "event", action: "broken", event: `{"focused_pane_id":"wrong","pane_id":"fallback","data":{"pane_id":"event"}}`, want: "event"},
		{name: "event root fallback", command: "event", event: `{"pane_id":"root"}`, want: "root"},
		{name: "explicit event skips malformed payload", command: "event", explicit: "explicit", event: "broken", want: "explicit"},
		{name: "missing action pane", command: "refresh", missing: true},
		{name: "missing watch pane", command: "watch", action: `{}`, missing: true},
		{name: "empty event is ignored", command: "event"},
		{name: "focused pane is not an event target", command: "event", event: `{"focused_pane_id":"wrong"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(pluginContextEnv, test.action)
			t.Setenv(pluginEventEnv, test.event)
			pane := paneContext{Command: test.command, ExplicitPane: test.explicit}
			got, err := pane.resolve()
			if test.missing {
				if err == nil || !strings.Contains(err.Error(), "pane is required") {
					t.Fatalf("missing-pane error: %v", err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("got %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestPaneContextDecodeErrorPreservesCause(t *testing.T) {
	for _, command := range []string{"refresh", "event"} {
		t.Run(command, func(t *testing.T) {
			t.Setenv(pluginContextEnv, "broken")
			t.Setenv(pluginEventEnv, "broken")
			pane := paneContext{Command: command}
			_, err := pane.resolve()
			if _, ok := errors.AsType[*json.SyntaxError](err); !ok {
				t.Fatalf("lost JSON syntax error: %v", err)
			}
			environment := pluginContextEnv
			if command == "event" {
				environment = pluginEventEnv
			}
			if !strings.Contains(err.Error(), environment) {
				t.Fatalf("error does not identify its source: %v", err)
			}
		})
	}
}
