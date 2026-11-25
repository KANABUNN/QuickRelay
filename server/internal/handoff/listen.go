package handoff

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/user"
	"strconv"
	"time"
)

// Listen creates the Unix domain socket. Access control is the socket file:
// mode 0660, group set to group when given. A stale socket from a previous run
// is replaced; any other file at path is left alone and reported.
func Listen(path, group string) (net.Listener, error) {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("refusing to replace non-socket file at %s", path)
		}
		if err = os.Remove(path); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	// The service umask (0077) creates the socket owner-only; widen only to the group.
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (net.Listener, error) { ln.Close(); return nil, err }
	if group != "" {
		g, err := user.LookupGroup(group)
		if err != nil {
			return fail(fmt.Errorf("hand-off socket group: %w", err))
		}
		gid, err := strconv.Atoi(g.Gid)
		if err != nil {
			return fail(errors.New("hand-off socket group has a non-numeric id"))
		}
		if err = os.Chown(path, -1, gid); err != nil {
			return fail(fmt.Errorf("hand-off socket group: %w", err))
		}
		if err = os.Chmod(path, 0660); err != nil {
			return fail(err)
		}
	}
	return ln, nil
}

// Serve runs the hand-off API on ln until ctx ends.
func Serve(ctx context.Context, ln net.Listener, s *Server) error {
	server := &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: MaxWait + 15*time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 8192,
		// Waiting long-polls end with ctx instead of holding shutdown for their full wait.
		BaseContext: func(net.Listener) context.Context { return ctx }}
	done := make(chan error, 1)
	go func() { done <- server.Serve(ln) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		server.Close()
	}
	<-done
	return nil
}
