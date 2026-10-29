package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	// ErrSocketNotFound is returned when connecting to a non-existent control socket.
	ErrSocketNotFound = errors.New("control socket not found")

	// ErrDaemonUnreachable is returned when connecting to the control socket fails.
	ErrDaemonUnreachable = errors.New("nieblafs daemon unreachable on control socket")
)

// Request defines the JSON IPC payload sent to the nieblafs control socket.
type Request struct {
	Action       string   `json:"action"` // "ping", "reanchor", "invalidate", "status"
	RootTreeHash string   `json:"root_tree_hash,omitempty"`
	CLID         int64    `json:"cl_id,omitempty"`
	Paths        []string `json:"paths,omitempty"`
}

// Response defines the JSON IPC payload returned from the nieblafs control socket.
type Response struct {
	Status        string   `json:"status"` // "ok", "pong", "error"
	Error         string   `json:"error,omitempty"`
	PurgedEntries int      `json:"purged_entries,omitempty"`
	RootTreeHash  string   `json:"root_tree_hash,omitempty"`
	CachedEntries int      `json:"cached_entries,omitempty"`
	CLID          int64    `json:"cl_id,omitempty"`
	UptimeSeconds float64  `json:"uptime_seconds,omitempty"`
}

// Handler handles IPC control commands in the nieblafs daemon.
type Handler interface {
	HandlePing(ctx context.Context) error
	HandleReanchor(ctx context.Context, newRootTreeHash string, clID int64) (purged int, err error)
	HandleInvalidate(ctx context.Context, paths []string) (purged int, err error)
	HandleStatus(ctx context.Context) (Response, error)
}

// Server listens on a Unix domain socket and dispatches control requests to Handler.
type Server struct {
	socketPath string
	handler    Handler
	listener   net.Listener
	startTime  time.Time
	mu         sync.Mutex
	closed     bool
}

// NewServer creates a new control socket server.
func NewServer(socketPath string, handler Handler) *Server {
	return &Server{
		socketPath: socketPath,
		handler:    handler,
		startTime:  time.Now(),
	}
}

// Start begins listening on the Unix domain socket. Non-blocking; launches a background goroutine.
func (s *Server) Start() error {
	if s.socketPath == "" {
		return errors.New("socket path cannot be empty")
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0755); err != nil {
		return fmt.Errorf("failed to create socket directory: %w", err)
	}

	// Remove stale socket if present
	_ = os.Remove(s.socketPath)

	l, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on unix socket %s: %w", s.socketPath, err)
	}
	s.listener = l

	go s.serve()
	return nil
}

func (s *Server) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			continue
		}

		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	// Set deadline for request processing
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	var req Request
	dec := json.NewDecoder(conn)
	if err := dec.Decode(&req); err != nil {
		if !errors.Is(err, io.EOF) {
			_ = json.NewEncoder(conn).Encode(Response{
				Status: "error",
				Error:  fmt.Sprintf("invalid JSON payload: %v", err),
			})
		}
		return
	}

	ctx := context.Background()
	var resp Response

	switch req.Action {
	case "ping":
		if err := s.handler.HandlePing(ctx); err != nil {
			resp = Response{Status: "error", Error: err.Error()}
		} else {
			resp = Response{Status: "pong"}
		}

	case "reanchor":
		purged, err := s.handler.HandleReanchor(ctx, req.RootTreeHash, req.CLID)
		if err != nil {
			resp = Response{Status: "error", Error: err.Error()}
		} else {
			resp = Response{
				Status:        "ok",
				PurgedEntries: purged,
				RootTreeHash:  req.RootTreeHash,
				CLID:          req.CLID,
			}
		}

	case "invalidate":
		purged, err := s.handler.HandleInvalidate(ctx, req.Paths)
		if err != nil {
			resp = Response{Status: "error", Error: err.Error()}
		} else {
			resp = Response{
				Status:        "ok",
				PurgedEntries: purged,
			}
		}

	case "status":
		statusResp, err := s.handler.HandleStatus(ctx)
		if err != nil {
			resp = Response{Status: "error", Error: err.Error()}
		} else {
			statusResp.UptimeSeconds = time.Since(s.startTime).Seconds()
			if statusResp.Status == "" {
				statusResp.Status = "ok"
			}
			resp = statusResp
		}

	default:
		resp = Response{
			Status: "error",
			Error:  fmt.Sprintf("unknown control action %q", req.Action),
		}
	}

	_ = json.NewEncoder(conn).Encode(resp)
}

// Close terminates the listener and removes the socket file.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	var err error
	if s.listener != nil {
		err = s.listener.Close()
	}
	_ = os.Remove(s.socketPath)
	return err
}

// Client communicates with a running nieblafs daemon over its control socket.
type Client struct {
	socketPath string
	timeout    time.Duration
}

// NewClient creates a new control client targeting socketPath.
func NewClient(socketPath string) *Client {
	return &Client{
		socketPath: socketPath,
		timeout:    3 * time.Second,
	}
}

func (c *Client) send(ctx context.Context, req Request) (*Response, error) {
	if _, err := os.Stat(c.socketPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrSocketNotFound, c.socketPath)
		}
		return nil, fmt.Errorf("%w: %v", ErrDaemonUnreachable, err)
	}

	dialer := net.Dialer{Timeout: c.timeout}
	conn, err := dialer.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDaemonUnreachable, err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(c.timeout))
	}

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, fmt.Errorf("failed to send control request: %w", err)
	}

	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, fmt.Errorf("failed to decode control response: %w", err)
	}

	if resp.Status == "error" {
		return nil, fmt.Errorf("nieblafs control error: %s", resp.Error)
	}

	return &resp, nil
}

// Ping checks if the nieblafs daemon is responsive.
func (c *Client) Ping(ctx context.Context) error {
	resp, err := c.send(ctx, Request{Action: "ping"})
	if err != nil {
		return err
	}
	if resp.Status != "pong" {
		return fmt.Errorf("unexpected ping response: %s", resp.Status)
	}
	return nil
}

// Reanchor instructs the nieblafs daemon to purge its cached inodes and point to newRootTreeHash.
func (c *Client) Reanchor(ctx context.Context, newRootTreeHash string, clID int64) (*Response, error) {
	return c.send(ctx, Request{
		Action:       "reanchor",
		RootTreeHash: newRootTreeHash,
		CLID:         clID,
	})
}

// Invalidate instructs the nieblafs daemon to purge specific path entries from its cache.
func (c *Client) Invalidate(ctx context.Context, paths []string) (*Response, error) {
	return c.send(ctx, Request{
		Action: "invalidate",
		Paths:  paths,
	})
}

// Status returns runtime statistics from the nieblafs daemon.
func (c *Client) Status(ctx context.Context) (*Response, error) {
	return c.send(ctx, Request{Action: "status"})
}
