// Package redistest starts process-owned Redis instances for mail integration tests.
package redistest

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
)

type Server struct {
	Path, Dir, Addr string
	port            int
	cmd             *exec.Cmd
	done            chan error
	log             *os.File
}

func Start(t *testing.T) *Server {
	t.Helper()
	path := os.Getenv("AIFD_REDIS_SERVER")
	if path == "" {
		path, _ = exec.LookPath("redis-server")
	}
	if path == "" {
		if os.Getenv("AIFD_MAIL_REDIS_REQUIRED") == "1" {
			t.Fatal("real Redis required: set AIFD_REDIS_SERVER")
		}
		t.Skip("set AIFD_REDIS_SERVER to run real Redis tests")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	s := &Server{Path: path, Dir: t.TempDir(), Addr: fmt.Sprintf("127.0.0.1:%d", port), port: port}
	s.start(t)
	t.Cleanup(func() { s.Stop(t) })
	return s
}

func (s *Server) start(t *testing.T) {
	t.Helper()
	log, err := os.OpenFile(filepath.Join(s.Dir, "redis-process.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	s.log = log
	s.cmd = exec.Command(s.Path, "--bind", "127.0.0.1", "--port", strconv.Itoa(s.port), "--dir", filepath.ToSlash(s.Dir), "--appendonly", "yes", "--appendfsync", "always", "--save", "")
	s.cmd.Stdout = log
	s.cmd.Stderr = log
	hideWindow(s.cmd)
	if err := s.cmd.Start(); err != nil {
		log.Close()
		t.Fatal(err)
	}
	s.done = make(chan error, 1)
	go func() { s.done <- s.cmd.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := redis.NewClient(&redis.Options{Addr: s.Addr, MaxRetries: -1, DialTimeout: 50 * time.Millisecond, ReadTimeout: 50 * time.Millisecond})
	defer client.Close()
	for {
		if client.Ping(ctx).Err() == nil {
			t.Logf("real Redis ready at %s", s.Addr)
			return
		}
		select {
		case err := <-s.done:
			raw, _ := os.ReadFile(filepath.Join(s.Dir, "redis-process.log"))
			s.cmd = nil
			s.log.Close()
			t.Fatalf("Redis exited: %v\n%s", err, raw)
		case <-ctx.Done():
			s.cmd.Process.Kill()
			<-s.done
			s.cmd = nil
			s.log.Close()
			raw, _ := os.ReadFile(filepath.Join(s.Dir, "redis-process.log"))
			t.Fatalf("Redis readiness: %v\n%s", ctx.Err(), raw)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (s *Server) Stop(t *testing.T) {
	t.Helper()
	if s.cmd == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client := redis.NewClient(&redis.Options{Addr: s.Addr, MaxRetries: -1, ReadTimeout: time.Second, WriteTimeout: time.Second})
	_ = client.Shutdown(ctx).Err()
	client.Close()
	select {
	case err := <-s.done:
		if err != nil {
			t.Errorf("Redis exit: %v", err)
		}
	case <-ctx.Done():
		s.cmd.Process.Kill()
		<-s.done
		t.Errorf("Redis shutdown timeout")
	}
	s.log.Close()
	s.cmd = nil
}

func (s *Server) Restart(t *testing.T) { t.Helper(); s.Stop(t); s.start(t) }
