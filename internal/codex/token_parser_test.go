package codex

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type cancelOnRead struct {
	io.Reader
	cancel context.CancelFunc
}

func (reader cancelOnRead) Read(buffer []byte) (int, error) {
	n, err := reader.Reader.Read(buffer)
	reader.cancel()
	return n, err
}

func TestParseRolloutCancellationDuringRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := cancelOnRead{Reader: strings.NewReader(`{"type":"turn_context","payload":{"model":"gpt-6.1-sol"}}` + "\n"), cancel: cancel}
	usage, err := ParseRollout(ctx, reader)
	if !errors.Is(err, context.Canceled) || usage != nil {
		t.Fatalf("usage=%+v error=%v", usage, err)
	}
}
