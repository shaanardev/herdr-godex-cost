package refresh

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sarora/herdr-godex-cost/internal/herder"
)

func TestRefreshDisplaysParentReviewsAndNestedSubagentsInOneTotal(t *testing.T) {
	home, state := t.TempDir(), t.TempDir()
	// Reviews are model changes within each rollout. The inherited fork counters
	// in the child are already part of the parent's usage and must not be billed again.
	rollouts := map[string]string{
		"root": `{"type":"session_meta","payload":{"id":"root"}}
{"type":"turn_context","payload":{"model":"gpt-6.1-sol"}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000000,"output_tokens":100000}}}}
{"type":"turn_context","payload":{"model":"codex-auto-review"}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1100000,"output_tokens":110000}}}}
`,
		"child": `{"type":"session_meta","payload":{"id":"child","parent_thread_id":"root","forked_from_id":"root","thread_source":"subagent"}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000000,"output_tokens":100000}}}}
{"type":"turn_context","payload":{"model":"gpt-6.1-sol"}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":2000000,"output_tokens":200000}}}}
{"type":"turn_context","payload":{"model":"codex-auto-review"}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":2100000,"output_tokens":210000}}}}
`,
		"grandchild": `{"type":"session_meta","payload":{"id":"grandchild","parent_thread_id":"child"}}
{"type":"turn_context","payload":{"model":"gpt-6.1-terra"}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000000,"output_tokens":100000}}}}
`,
	}
	sessions := filepath.Join(home, "sessions")
	if err := os.MkdirAll(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	for id, log := range rollouts {
		if err := os.WriteFile(filepath.Join(sessions, "rollout-"+id+".jsonl"), []byte(log), 0600); err != nil {
			t.Fatal(err)
		}
	}
	directory := t.TempDir()
	executable := filepath.Join(directory, "herdr")
	reports := filepath.Join(directory, "reports")
	script := `#!/bin/sh
case "$1 $2" in
 "pane get") printf '%s\n' '{"result":{"pane":{"agent":"codex","agent_session":{"agent":"codex","kind":"id","value":"root"}}}}' ;;
 "pane report-metadata")
  for arg in "$@"; do case "$arg" in cost=*) printf '%s\n' "$arg" >> "$TEST_TOTAL_REPORTS" ;; esac; done ;;
 *) exit 7 ;;
esac
`
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_TOTAL_REPORTS", reports)
	runner := NewRunner(herder.NewClient(executable), home, state, 0)
	// Parent $3 + parent review $0.40 + child $3 + child review $0.40
	// + grandchild $1.50 = $8.30. The second refresh exercises the cached path.
	for range 2 {
		if err := runner.Refresh(context.Background(), "pane"); err != nil {
			t.Fatal(err)
		}
	}
	content, err := os.ReadFile(reports)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "cost=$8.30\ncost=$8.30\n" {
		t.Fatalf("cost components were omitted or counted twice: %s", content)
	}
	paths, err := filepath.Glob(filepath.Join(state, "*.json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("cache files=%v: %v", paths, err)
	}
	contents, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	var cached cacheEnvelope
	if err := json.Unmarshal(contents, &cached); err != nil {
		t.Fatal(err)
	}
	if cached.Value == nil || cached.Value.KnownUSD != 7.5 || cached.Value.EstimatedUSD != .8 {
		t.Fatalf("cached cost categories are incorrect: %+v", cached.Value)
	}
	// If a descendant has unknown pricing, keep the priced subtotal visibly partial.
	path := filepath.Join(sessions, "rollout-grandchild.jsonl")
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(rollouts["grandchild"], "gpt-6.1-terra", "unknown")), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runner.Refresh(context.Background(), "pane"); err != nil {
		t.Fatal(err)
	}
	content, err = os.ReadFile(reports)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(content), "cost=~$6.80\n") {
		t.Fatalf("unknown descendant pricing lost the partial total: %s", content)
	}
}
