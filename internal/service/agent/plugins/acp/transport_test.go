package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"
)

type nopWriteCloser struct {
	io.Writer
}

func (n nopWriteCloser) Close() error { return nil }

type nopReadCloser struct {
	io.Reader
}

func (n nopReadCloser) Close() error { return nil }

func TestACPTransportRequestNotificationAndRPCError(t *testing.T) {
	clientToServerR, clientToServerW := io.Pipe()
	serverToClientR, serverToClientW := io.Pipe()
	defer clientToServerR.Close()
	defer clientToServerW.Close()
	defer serverToClientR.Close()
	defer serverToClientW.Close()

	notifications := make(chan string, 1)
	transport := newACPTransport(clientToServerW, serverToClientR, func(method string, params json.RawMessage) {
		notifications <- method
	})
	transport.Start()
	defer transport.Close()

	go func() {
		scanner := bufio.NewScanner(clientToServerR)
		for scanner.Scan() {
			var req struct {
				ID     uint64          `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			_ = json.Unmarshal(scanner.Bytes(), &req)
			if req.Method == "fail" {
				_, _ = serverToClientW.Write([]byte(`{"jsonrpc":"2.0","id":` + itoa(req.ID) + `,"error":{"code":-32602,"message":"bad params"}}` + "\n"))
				continue
			}
			_, _ = serverToClientW.Write([]byte(`{"jsonrpc":"2.0","method":"session/update","params":{"ok":true}}` + "\n"))
			_, _ = serverToClientW.Write([]byte(`{"jsonrpc":"2.0","id":` + itoa(req.ID) + `,"result":{"ok":true}}` + "\n"))
		}
	}()

	result, err := transport.SendRequestContext(context.Background(), "ok", map[string]string{"hello": "world"})
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	if !strings.Contains(string(result), `"ok":true`) {
		t.Fatalf("unexpected result: %s", result)
	}
	select {
	case method := <-notifications:
		if method != "session/update" {
			t.Fatalf("unexpected notification: %s", method)
		}
	case <-time.After(time.Second):
		t.Fatal("notification not delivered")
	}

	if _, err := transport.SendRequestContext(context.Background(), "fail", nil); err == nil || !strings.Contains(err.Error(), "bad params") {
		t.Fatalf("expected rpc error, got %v", err)
	}
}

func TestACPTransportTimeoutAndWriteError(t *testing.T) {
	silent := newACPTransport(nopWriteCloser{Writer: io.Discard}, nopReadCloser{Reader: strings.NewReader("")}, nil)
	silent.Start()
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	if _, err := silent.SendRequestContext(ctx, "timeout", nil); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got %v", err)
	}
	_ = silent.Close()

	closedR, closedW := io.Pipe()
	_ = closedR.Close()
	_ = closedW.Close()
	transport := newACPTransport(closedW, nopReadCloser{Reader: strings.NewReader("")}, nil)
	if _, err := transport.SendRequest("write", nil); err == nil || !strings.Contains(err.Error(), "write request") {
		t.Fatalf("expected write error, got %v", err)
	}
}

func itoa(v uint64) string {
	return strconv.FormatUint(v, 10)
}
