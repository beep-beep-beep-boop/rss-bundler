package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/beep-beep-beep-boop/rss-bundler/internal/bundler"
	"github.com/beep-beep-beep-boop/rss-bundler/internal/cache"
	"github.com/beep-beep-beep-boop/rss-bundler/internal/common"
	"github.com/beep-beep-beep-boop/rss-bundler/internal/entry_timeout"
	"github.com/thejerf/suture/v4"
	"github.com/thejerf/sutureslog"
)

func main() {
	log := slog.Default()
	soup_logger := sutureslog.Handler{
		Logger: log.WithGroup("soup"),

		StopTimeoutLevel:      slog.LevelInfo,
		ServicePanicLevel:     slog.LevelError,
		ServiceTerminateLevel: slog.LevelError,
		BackoffLevel:          slog.LevelInfo,
		ResumeLevel:           slog.LevelDebug,
	}

	soup := suture.New("soupervisor", suture.Spec{
		EventHook: soup_logger.MustHook(),
	})

	ctx, cancel := context.WithCancel(context.Background())

	soup.ServeBackground(ctx)

	config, err := common.MakeConfig(ctx)
	if err != nil {
		log.Error("configuration error", "err", err)
		os.Exit(1)
	}

	if config.Debug {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}

	app := startServices(log, soup, config)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "Content-type: text/xml;charset=UTF-8")
		rss_str, ok := app.GetFeed()
		if ok {
			w.WriteHeader(http.StatusOK)
			_, err := fmt.Fprint(w, rss_str)
			if err != nil {
				log.Error("write error writing to http.ResponseWriter", "err", err)
			}
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	})

	server := &http.Server{Addr: config.Listen_addr}

	// start the server
	go func() {
		err := server.ListenAndServe()
		if err != nil {
			log.Error("http server failed to listen", "err", err)
			os.Exit(1)
		}
	}()
	log.Info("http server is listening for requests :3", "address", config.Listen_addr)

	// stop the server once ctx is done
	context.AfterFunc(ctx, func() {
		// allow it to take 5 seconds before forcibly shutting down.
		deadline, _ := context.WithDeadlineCause(
			context.Background(),
			time.Now().Add(time.Second*5),
			errors.New("http server took too long to close"),
		)
		err := server.Shutdown(deadline)
		if err != nil {
			log.Error("error shutting down http server", "err", err)
		}
	})

	should_exit := make(chan os.Signal, 1)
	signal.Notify(should_exit, os.Interrupt, syscall.SIGTERM)

	<-should_exit
	log.Info("recieved exit signal, closing...")
	cancel()
}

func startServices(
	l *slog.Logger,
	soup *suture.Supervisor,
	config common.Config,
) *cache.Cache {

	_entry_timeout := entry_timeout.New(
		l.WithGroup("entry_timeout"),
		config,
	)

	_bundler := bundler.New(
		l.WithGroup("bundler"),
		config,
		_entry_timeout,
	)

	_cache := cache.New(
		l.WithGroup("cache"),
		config,
		_bundler,
	)

	soup.Add(_entry_timeout)
	soup.Add(_bundler)
	soup.Add(_cache)

	return _cache
}
