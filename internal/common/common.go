package common

import (
	"context"
	"errors"
	"time"

	"github.com/sethvargo/go-envconfig"
)

type Config struct {
	// the list of feeds to bundle into one feed.
	Feed_urls []string `env:"FEED_URLS, required"`
	// how often to fetch all of the feeds and update our bundled one.
	Fetch_interval time.Duration `env:"FETCH_INTERVAL, default=1h"`
	// timout when fetchinig a single feed.
	Fetch_timeout time.Duration `env:"FETCH_TIMEOUT, default=1m"`
	// address the server should listen on.
	Listen_addr string `env:"LISTEN_ADDR, default=:3000"`

	// the title for the output feed.
	Out_title string `env:"OUTFEED_TITLE, default=bundle"`
	// the link for the output feed.
	Out_link string `env:"OUTFEED_LINK, default="`
	// the description for the output feed.
	Out_description string `env:"OUTFEED_DESCRIPTION, default=this is a bundle feed, which contains the entries from multiple rss feeds."`
	// the author name for the output feed.
	Out_author_name string `env:"OUTFEED_AUTHOR, default=bundler robot"`
	// the author email for the output feed.
	Out_author_email string `env:"OUTFEED_AUTHOR_EMAIL, default="`

	// how often to re-create the error entry for failing feeds. (when we fail
	// to fetch a feed, we add an entry to the output rss saying we failed to
	// fetch it).
	Out_error_cooldown time.Duration `env:"OUTFEED_ERROR_COOLDOWN, default=24h"`

	// whether to do debug logging.
	Debug bool `env:"DEBUG, default=false"`
}

func MakeConfig(ctx context.Context) (Config, error) {
	var config Config

	err := envconfig.Process(ctx, &config)
	if err != nil {
		return config, err
	}

	return config, nil
}

var ErrTimeout = errors.New("actor channel response/request timed out")
