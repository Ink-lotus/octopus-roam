//go:build !windows

package desktop

import "path/filepath"

func canonicalPath(path string) (string, error) { return filepath.EvalSymlinks(path) }

// Native desktop control is Windows-only; other platforms keep signal shutdown.
type Server struct{}

func BeginServer() (*Server, error)   { return &Server{}, nil }
func (*Server) Stop() <-chan struct{} { return nil }
func (*Server) Ready() error          { return nil }
func (*Server) Close()                {}
