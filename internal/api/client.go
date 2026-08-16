package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
)

var (
	// ErrProtocol reports a daemon speaking a different wire version.
	ErrProtocol = errors.New("protocol version mismatch")
	// ErrDaemon reports a RepError the daemon sent for a command.
	ErrDaemon = errors.New("daemon refused the command")
)

// Client is one connection to wavezd. Commands are correlated by id, so a
// caller may issue them concurrently while pushed replies stream on Events.
// Close releases the connection; a Client is not reusable after it.
type Client struct {
	conn    net.Conn
	enc     *json.Encoder
	events  chan Reply
	pending map[string]chan Reply
	done    chan struct{}

	err    atomic.Pointer[error]
	seq    atomic.Uint64
	mu     sync.Mutex
	closed atomic.Bool
}

// closeQuietly drops a half-built client; the dial error is the one to report.
func closeQuietly(c *Client) {
	if err := c.Close(); err != nil {
		_ = err
	}
}

// Dial connects to the daemon's socket and completes the handshake, failing
// with ErrProtocol rather than letting a version gap surface later as a
// confusing decode error.
func Dial(ctx context.Context, sockPath string) (*Client, error) {
	var d net.Dialer

	conn, err := d.DialContext(ctx, "unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("dialing %s: %w", sockPath, err)
	}

	c := &Client{
		conn:    conn,
		enc:     json.NewEncoder(conn),
		events:  make(chan Reply, eventBuffer),
		pending: make(map[string]chan Reply),
		done:    make(chan struct{}),
	}
	go c.read()

	hello, err := c.Do(ctx, Command{Kind: CmdHello})
	if err != nil {
		closeQuietly(c)

		return nil, err
	}
	if hello.Protocol != Protocol {
		closeQuietly(c)

		return nil, fmt.Errorf("%w: daemon speaks %d, client speaks %d", ErrProtocol, hello.Protocol, Protocol)
	}

	return c, nil
}

const eventBuffer = 256

// Events streams replies the daemon pushes on its own, which is every event a
// subscription delivers. The channel closes when the connection ends.
func (c *Client) Events() <-chan Reply { return c.events }

// Do sends cmd and waits for the reply carrying its id. A RepError reply
// becomes a Go error, so a caller checks one thing rather than two.
func (c *Client) Do(ctx context.Context, cmd Command) (Reply, error) {
	if c.closed.Load() {
		return Reply{}, net.ErrClosed
	}
	if cmd.ID == "" {
		cmd.ID = "c" + strconv.FormatUint(c.seq.Add(1), 36)
	}

	waiter := make(chan Reply, 1)
	c.mu.Lock()
	c.pending[cmd.ID] = waiter
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, cmd.ID)
		c.mu.Unlock()
	}()

	if err := c.enc.Encode(cmd); err != nil {
		return Reply{}, fmt.Errorf("sending %s: %w", cmd.Kind, err)
	}

	select {
	case reply := <-waiter:
		if reply.Kind == RepError {
			return reply, fmt.Errorf("%w: %s: %s", ErrDaemon, cmd.Kind, reply.Error)
		}

		return reply, nil
	case <-c.done:
		return Reply{}, c.readErr()
	case <-ctx.Done():
		return Reply{}, fmt.Errorf("waiting for %s: %w", cmd.Kind, ctx.Err())
	}
}

func (c *Client) readErr() error {
	if p := c.err.Load(); p != nil && *p != nil {
		return *p
	}

	return net.ErrClosed
}

func (c *Client) read() {
	defer close(c.done)
	defer close(c.events)

	sc := bufio.NewScanner(c.conn)
	sc.Buffer(make([]byte, 0, initialLine), maxLine)

	for sc.Scan() {
		var reply Reply
		if err := json.Unmarshal(sc.Bytes(), &reply); err != nil {
			wrapped := fmt.Errorf("decoding reply: %w", err)
			c.err.Store(&wrapped)

			return
		}
		c.deliver(reply)
	}
	if err := sc.Err(); err != nil {
		wrapped := fmt.Errorf("reading socket: %w", err)
		c.err.Store(&wrapped)
	}
}

func (c *Client) deliver(reply Reply) {
	if reply.ID != "" {
		c.mu.Lock()
		waiter, ok := c.pending[reply.ID]
		c.mu.Unlock()

		if ok {
			waiter <- reply

			return
		}
	}

	select {
	case c.events <- reply:
	default:
	}
}

const (
	initialLine = 64 << 10
	maxLine     = 8 << 20
)

// Close ends the connection. It is safe to call more than once.
func (c *Client) Close() error {
	if c.closed.Swap(true) {
		return nil
	}
	if err := c.conn.Close(); err != nil {
		return fmt.Errorf("closing connection: %w", err)
	}

	return nil
}
