package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"showoff-golang/internal/httpapp"
	"showoff-golang/internal/metricscollector"
)

const defaultAddr = ":8080"
const shutdownTimeout = 10 * time.Second

var signalNotifyContext = signal.NotifyContext
var fatalf = log.Fatalf
var newPostgresHandler = httpapp.NewPostgresHandler
var serverListenAndServe = func(s *http.Server) error { return s.ListenAndServe() }
var serverShutdown = func(s *http.Server, ctx context.Context) error { return s.Shutdown(ctx) }

func run() error {
	handler := httpapp.NewHandler()
	var closeMetricsStoreFn func() error

	if databaseURL := os.Getenv("DATABASE_URL"); databaseURL != "" {
		dbHandler, closeFn, err := newPostgresHandler(context.Background(), databaseURL)
		if err != nil {
			return err
		}
		defer func() { _ = closeFn() }()
		handler = dbHandler
	}

	var metricsStore metricscollector.Store
	if redisAddr := os.Getenv("METRICS_REDIS_ADDR"); redisAddr != "" {
		redisStore := metricscollector.NewRedisStore(redisAddr, os.Getenv("METRICS_REDIS_PASSWORD"), 0)
		metricsStore = redisStore
		closeMetricsStoreFn = redisStore.Close
	} else {
		metricsStore = metricscollector.NewMemoryStore()
	}
	if closeMetricsStoreFn != nil {
		defer func() { _ = closeMetricsStoreFn() }()
	}

	collector := metricscollector.New(metricscollector.Config{
		Workers:       2,
		QueueSize:     1024,
		FlushInterval: 1 * time.Second,
	}, metricsStore)
	collector.Start(context.Background())
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = collector.Stop(stopCtx)
	}()

	rootMux := http.NewServeMux()
	metricsHandler := metricscollector.NewHandler(collector)
	rootMux.Handle("/", collector.Middleware(handler))
	rootMux.Handle("/metrics", metricsHandler)
	rootMux.Handle("/metrics/", metricsHandler)

	server := &http.Server{
		Addr:              defaultAddr,
		Handler:           rootMux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	shutdownSignalCtx, stop := signalNotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- serverListenAndServe(server)
	}()

	select {
	case err := <-serverErrCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-shutdownSignalCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := serverShutdown(server, shutdownCtx); err != nil {
			return err
		}

		err := <-serverErrCh
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func main() {
	if err := run(); err != nil {
		fatalf("server error: %v", err)
	}
}
