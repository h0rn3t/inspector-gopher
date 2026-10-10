package mcp

import (
	"context"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RunStdio serves the MCP server on stdin/stdout until ctx is cancelled.
func (s *Server) RunStdio(ctx context.Context) error {
	return s.mcpServer.Run(ctx, &mcp.StdioTransport{})
}

// httpHandler routes dashboard previews alongside the MCP endpoint. The MCP
// handler stays on "/" so existing clients keep working unchanged.
func (s *Server) httpHandler() http.Handler {
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return s.mcpServer
	}, &mcp.StreamableHTTPOptions{JSONResponse: true})
	mux := http.NewServeMux()
	mux.HandleFunc("/dashboard/flamegraph/{id}", s.handleDashboardFlamegraph)
	mux.HandleFunc("/dashboard/", s.handleDashboardIndex)
	mux.HandleFunc("/dashboard", s.handleDashboardIndex)
	mux.Handle("/", mcpHandler)
	return mux
}

// requireGetRead allows GET and HEAD on dashboard pages; any other method
// is a 405. Handlers are registered without a method pattern (a GET pattern
// would let other methods fall through to the MCP catch-all), so the check
// lives here.
func requireGetRead(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET")
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

// RunHTTP serves Streamable HTTP plus dashboard previews on addr until ctx
// is cancelled.
func (s *Server) RunHTTP(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: s.httpHandler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
