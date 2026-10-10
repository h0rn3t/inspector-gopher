// DAP wire protocol: minimal framing over TCP (Content-Length headers,
// request/response matching, async stopped/terminated events).
package debug

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"encoding/json/jsontext"
	json "encoding/json/v2"
)

const (
	dapContentLength = "Content-Length:"
	dapHeaderEnd     = "\r\n\r\n"
	maxDAPBody       = 16 << 20
)

// dapResponse is a DAP response message.
type dapResponse struct {
	Seq        int            `json:"seq"`
	Type       string         `json:"type"`
	RequestSeq int            `json:"request_seq"`
	Success    bool           `json:"success"`
	Command    string         `json:"command"`
	Message    string         `json:"message,omitempty"`
	Body       jsontext.Value `json:"body,omitempty"`
}

// dapEvent is a DAP event message.
type dapEvent struct {
	Seq   int            `json:"seq"`
	Type  string         `json:"type"`
	Event string         `json:"event"`
	Body  jsontext.Value `json:"body,omitempty"`
}

// dapClient speaks request/response DAP over a single TCP conn.
type dapClient struct {
	conn    net.Conn
	reader  *bufio.Reader
	writeMu sync.Mutex
	seq     atomic.Int64
	pending sync.Map // int -> chan dapResponse
	events  chan dapEvent
	closed  atomic.Bool
	done    chan struct{}
}

func newDAPClient(conn net.Conn) *dapClient {
	c := &dapClient{conn: conn, reader: bufio.NewReader(conn), events: make(chan dapEvent, 16), done: make(chan struct{})}
	go c.run()
	return c
}

func dialTCP(ctx context.Context, addr string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "tcp", addr)
}

func (c *dapClient) close() {
	if c.closed.Swap(true) {
		return
	}
	close(c.done)
	_ = c.conn.Close()
}

func (c *dapClient) run() {
	defer c.close()
	for {
		msg, err := c.readMessage()
		if err != nil {
			if !c.closed.Load() {
				c.failPending(err)
			}
			return
		}
		var probe struct {
			Type       string `json:"type"`
			Command    string `json:"command"`
			Event      string `json:"event"`
			RequestSeq int    `json:"request_seq"`
		}
		if err := json.Unmarshal(msg, &probe); err != nil {
			continue
		}
		switch probe.Type {
		case "response":
			var resp dapResponse
			if err := json.Unmarshal(msg, &resp); err != nil {
				continue
			}
			if ch, ok := c.pending.LoadAndDelete(resp.RequestSeq); ok {
				select {
				case ch.(chan dapResponse) <- resp:
				case <-c.done:
				}
			}
		case "event":
			var ev dapEvent
			if err := json.Unmarshal(msg, &ev); err != nil {
				continue
			}
			select {
			case c.events <- ev:
			default:
			}
		}
	}
}

func (c *dapClient) failPending(err error) {
	c.pending.Range(func(k, v any) bool {
		ch := v.(chan dapResponse)
		select {
		case ch <- dapResponse{Success: false, Message: err.Error()}:
		default:
		}
		c.pending.Delete(k)
		return true
	})
}

func (c *dapClient) readMessage() ([]byte, error) {
	contentLen := -1
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(line, dapContentLength) {
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, dapContentLength)))
			if err != nil || n < 0 || n > maxDAPBody {
				return nil, fmt.Errorf("bad Content-Length %q", line)
			}
			contentLen = n
		}
	}
	if contentLen < 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}
	body := make([]byte, contentLen)
	if _, err := io.ReadFull(c.reader, body); err != nil {
		return nil, err
	}
	return body, nil
}

// send issues one DAP request and waits for its response.
func (c *dapClient) send(ctx context.Context, command string, args any) (jsontext.Value, error) {
	seq := int(c.seq.Add(1))
	req := map[string]any{"seq": seq, "type": "request", "command": command}
	if args != nil {
		req["arguments"] = args
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	ch := make(chan dapResponse, 1)
	c.pending.Store(seq, ch)
	defer c.pending.Delete(seq)

	c.writeMu.Lock()
	_, werr := fmt.Fprintf(c.conn, "Content-Length: %d%s%s", len(body), dapHeaderEnd, body)
	c.writeMu.Unlock()
	if werr != nil {
		return nil, werr
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		select {
		case resp := <-ch:
			if !resp.Success {
				return nil, fmt.Errorf("dap %s: %s", command, resp.Message)
			}
			return resp.Body, nil
		default:
			return nil, fmt.Errorf("dap %s: connection closed", command)
		}
	case resp := <-ch:
		if !resp.Success {
			return nil, fmt.Errorf("dap %s: %s", command, resp.Message)
		}
		return resp.Body, nil
	}
}

// waitEvent waits for the next event with the given name.
func (c *dapClient) waitEvent(ctx context.Context, name string) (dapEvent, error) {
	for {
		select {
		case <-ctx.Done():
			return dapEvent{}, ctx.Err()
		case <-c.done:
			return dapEvent{}, fmt.Errorf("connection closed while waiting for %q", name)
		case ev := <-c.events:
			if ev.Event == name {
				return ev, nil
			}
		}
	}
}

// drainEvents drops buffered events (e.g. before continue).
func (c *dapClient) drainEvents() {
	for {
		select {
		case <-c.events:
		default:
			return
		}
	}
}
