package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/abd-im-cli/abdim-cli/internal/contracts"
)

// Handler serves one validated local RPC request.
type Handler func(context.Context, contracts.Request) (contracts.Response, error)

// Server accepts only local Unix socket RPC connections.
type Server struct {
	listener *net.UnixListener
	handler  Handler

	mu     sync.Mutex
	closed bool
	paths  sync.WaitGroup
}

// Listen creates an owner-only Unix domain socket. It never binds a TCP port.
func Listen(path string, handler Handler) (*Server, error) {
	if path == "" {
		return nil, errors.New("socket path is required")
	}
	if handler == nil {
		return nil, errors.New("RPC handler is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create socket directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("secure socket directory: %w", err)
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("refusing to replace non-socket %q", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale socket: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect socket path: %w", err)
	}

	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("listen on Unix socket: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("secure socket: %w", err)
	}
	listener.SetUnlinkOnClose(true)
	return &Server{listener: listener, handler: handler}, nil
}

// Serve accepts connections until Close is called or context is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		_ = s.Close()
	}()

	for {
		connection, err := s.listener.AcceptUnix()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed || errors.Is(err, net.ErrClosed) {
				s.paths.Wait()
				return nil
			}
			return fmt.Errorf("accept local RPC connection: %w", err)
		}
		s.paths.Add(1)
		go func() {
			defer s.paths.Done()
			defer connection.Close()
			s.serveConnection(ctx, connection)
		}()
	}
}

// Close stops accepting new connections and removes the socket pathname.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	listener := s.listener
	s.mu.Unlock()
	if listener == nil {
		return nil
	}
	return listener.Close()
}

func (s *Server) serveConnection(ctx context.Context, connection *net.UnixConn) {
	for {
		payload, err := ReadFrame(connection)
		if err != nil {
			return
		}
		var request contracts.Request
		if err := json.Unmarshal(payload, &request); err != nil {
			_ = writeResponse(connection, invalidRequestResponse("", "request must be valid JSON"))
			continue
		}
		if err := request.Validate(); err != nil {
			_ = writeResponse(connection, invalidRequestResponse(request.RequestID, err.Error()))
			continue
		}

		response, err := s.handler(ctx, request)
		if err != nil {
			response = internalErrorResponse(request.RequestID, err)
		}
		if err := response.Validate(); err != nil {
			response = internalErrorResponse(request.RequestID, fmt.Errorf("handler returned invalid response: %w", err))
		}
		if err := writeResponse(connection, response); err != nil {
			return
		}
	}
}

func writeResponse(writer *net.UnixConn, response contracts.Response) error {
	payload, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return WriteFrame(writer, payload)
}

func invalidRequestResponse(requestID, message string) contracts.Response {
	return errorResponse(responseRequestID(requestID), contracts.CodeInvalidArgument, message, false)
}

func internalErrorResponse(requestID string, err error) contracts.Response {
	return errorResponse(requestID, contracts.CodeInternal, "local RPC handler failed", false)
}

func errorResponse(requestID string, code contracts.ErrorCode, message string, retryable bool) contracts.Response {
	return contracts.Response{
		APIVersion: contracts.APIVersionV1,
		RequestID:  requestID,
		OK:         false,
		Error:      &contracts.Error{Code: code, Message: message, Retryable: retryable},
	}
}

func responseRequestID(requestID string) string {
	if requestID == "" {
		return "invalid"
	}
	return requestID
}

// Call connects to a local Unix socket and exchanges one framed request.
func Call(ctx context.Context, path string, request contracts.Request) (contracts.Response, error) {
	if err := request.Validate(); err != nil {
		return contracts.Response{}, err
	}
	dialer := net.Dialer{}
	connection, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return contracts.Response{}, fmt.Errorf("connect to local RPC: %w", err)
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			return contracts.Response{}, fmt.Errorf("set local RPC deadline: %w", err)
		}
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return contracts.Response{}, fmt.Errorf("encode local RPC request: %w", err)
	}
	if err := WriteFrame(connection, payload); err != nil {
		return contracts.Response{}, err
	}
	payload, err = ReadFrame(connection)
	if err != nil {
		return contracts.Response{}, err
	}
	var response contracts.Response
	if err := json.Unmarshal(payload, &response); err != nil {
		return contracts.Response{}, fmt.Errorf("decode local RPC response: %w", err)
	}
	if err := response.Validate(); err != nil {
		return contracts.Response{}, fmt.Errorf("invalid local RPC response: %w", err)
	}
	return response, nil
}
