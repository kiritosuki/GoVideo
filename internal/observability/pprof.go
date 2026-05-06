package observability

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"time"
)

type PprofServer struct {
	name            string
	server          *http.Server
	shutdownTimeout time.Duration
}

// NewPprofMux 创建pprof路由
func NewPprofMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return mux
}

// NewPprofServer 创建pprofServer 并开启server后台协程监听服务
func NewPprofServer(name string, enabled bool, addr string) (*PprofServer, error) {
	if !enabled || addr == "" {
		return nil, nil
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to start %s pprof server on %s: %w", name, addr, err)
	}
	pprofServer := &PprofServer{
		name:            name,
		shutdownTimeout: 3 * time.Second,
	}
	pprofServer.server = &http.Server{
		Addr:              addr,
		Handler:           NewPprofMux(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("%s pprof listening on %s\n", name, addr)
		// 正常关闭 ErrServerClosed 不打印错误日志
		if err := pprofServer.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("%s pprof server error: %v", name, err)
		}
	}()
	return pprofServer, nil
}

// Shutdown 实际执行server关闭的函数
func Shutdown(ctx context.Context, server *http.Server) error {
	if server == nil {
		return nil
	}
	return server.Shutdown(ctx)
}

// Close 关闭pprofServer的server 关闭用时3s超时后取消上下文 强制关闭
func (s *PprofServer) Close() error {
	if s == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
	defer cancel()
	if err := Shutdown(ctx, s.server); err != nil {
		log.Printf("failed to shutdown %s pprof server: %v", s.name, err)
		return err
	}
	return nil
}
