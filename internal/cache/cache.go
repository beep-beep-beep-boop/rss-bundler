package cache

import (
	"context"
	"log/slog"
	"time"

	"github.com/beep-beep-beep-boop/rss-bundler/internal/bundler"
	"github.com/beep-beep-beep-boop/rss-bundler/internal/common"
)

type Cache struct {
	log    *slog.Logger
	config common.Config

	cached_feed string

	// the service will use this to communicate with the bundleCreatorService
	bundler *bundler.Bundler

	// get the current bundled rss feed as a string.
	get_feed chan string
}

func New(
	log *slog.Logger,
	config common.Config,
	bundler *bundler.Bundler,
) *Cache {

	return &Cache{
		log:         log,
		config:      config,
		cached_feed: "",
		bundler:     bundler,
		get_feed:    make(chan string),
	}
}

func (e Cache) Serve(ctx context.Context) error {
	// update the cache right away once the service starts.
	update_cache := make(chan string)
	e.bundler.MakeBundle(update_cache)
	refetch := time.NewTicker(e.config.Fetch_interval)

	for {
		select {
		case <-refetch.C:
			e.bundler.MakeBundle(update_cache)
		case e.get_feed <- e.cached_feed:
		case f := <-update_cache:
			e.log.Debug("updated cached feed")
			e.cached_feed = f
		case <-ctx.Done():
			return nil
		}
	}
}

// gets the current cached rss feed as a string. returns true if the feed was
// gotten successfully, false if not.
func (e *Cache) GetFeed() (string, bool) {
	select {
	case feed := <-e.get_feed:
		return feed, true
	case <-time.After(time.Millisecond * 500):
		e.log.Error("failed to get cached feed")
		return "", false
	}
}
