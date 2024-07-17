package main

import (
	"context"
	"crypto/md5"
	"encoding/base32"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"

	"github.com/gorilla/feeds"
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

}

type rssBundler struct {
	log *slog.Logger
	// the list of the rss feed urls to bundle into one feed.
	feed_urls []string
	// the interval it should re-fetch the feeds at.
	fetch_interval time.Duration
	// how long it takes to timeout when fetching a single feed.
	fetch_timeout time.Duration

	// get the current bundled rss feed as a string.
	get_feed chan string
}

type rssBundlerService struct {
	rssBundler
	cached_feed  string
	update_cache chan string
}

func newBundler(
	l *slog.Logger,
	feed_urls []string,
	fetch_interval time.Duration,
	fetch_timeout time.Duration,
) (*rssBundler, error) {

	e := rssBundler{
		log:            l.WithGroup("bundler"),
		feed_urls:      feed_urls,
		fetch_interval: fetch_interval,
		fetch_timeout:  fetch_timeout,
		get_feed:       make(chan string),
	}

	return &e, nil
}

func (e rssBundlerService) Serve(ctx context.Context) error {
	refetch := time.NewTicker(e.fetch_interval)

	for {
		select {
		case <-refetch.C:
			go updateCache(
				ctx,
				e.log.WithGroup("updater"),
				e.feed_urls,
				e.fetch_timeout,
				e.update_cache,
			)
		case e.get_feed <- e.cached_feed:
		case f := <-e.update_cache:
			e.log.Debug("updated cached feed")
			e.cached_feed = f
		case <-ctx.Done():
			return nil
		}
	}
}

func updateCache(
	ctx context.Context,
	log *slog.Logger,
	urls []string,
	timeout time.Duration,
	send_result_to chan string,
) {
	var in_feeds []gofeed.Feed
	var errors []feeds.Item

	// first we fetch all the feeds from their urls.
	for _, url := range urls {
		feed, err := fetchFeed(
			ctx, log, url, timeout,
		)

		// if there was an error, create an entry that we will put in
		// the resulting rss feed.
		if err != nil {
			log.Info("failed to fetch feed", "url", url, "err", err)
			errors = append(errors, failedToFetch(url))
			continue
		}

		rfeed := feeds.New()
	}

	// if there's any errors in the error list, make a feed
	// of those and append it too.
}

func fetchFeed(
	ctx context.Context,
	log *slog.Logger,
	url string,
	timeout time.Duration,
) (*gofeed.Feed, error) {

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	log.Debug("fetching feed", "url", url)
	start := time.Now()

	fp := gofeed.NewParser()
	f, err := fp.ParseURLWithContext(url, ctx)
	log.Info("finished fetching feed", "url", url, "took", time.Since(start), "err", err)

	return f, err
}

func failedToFetch(url string) feeds.Item {
	now := time.Now()
	uuid := feeds.NewUUID()
	return feeds.Item{
		Title:   entryTitle("bundler errors", "failed to fetch feed :("),
		Content: fmt.Sprintf("url: %s", url),
		Created: now,
		Id:      uuid.String(),
	}
}

func entryTitle(feed_title string, entry_title string) string {
	if feed_title == nil && entry_title == nil {
		return nil
	}
	if feed_title == nil {
		return entry_title
	}
	if entry_title == nil {
		return feed_title
	}
	return fmt.Sprintf("%s | %s", feed_title, entry_title)
}

func feedsToItems(in_feeds []gofeed.Feed) []feeds.Item {
	var items []feeds.Item

	for _, f := range in_feeds {

	}

	return items
}

func feedToItems(in_feed gofeed.Feed) feeds.Item {
	var items []feeds.Item

	for _, fi := in_feed.Items {
		new_item := inItemToOutItem(fi)
		items = append(items, new_item)
	}
}

func inItemToOutItem(in gofeed.Item, feed_title string, feed_url string) feeds.Item {
	item := feeds.Item{
		Title: entryTitle(feed_title, in.Title),
		Link: &feeds.Link{Href: in.Link},
		Source: &feeds.Link{Href: feed_url},
		Author: &feeds.Author{Name: in.Author.Name, Email: in.Author.Email},
		Description: in.Description,
		Id: bundledGuid(feed_url, in.GUID),
		// we made our own guid, so the guid will never be a permalink.
		IsPermaLink: false,
		Updated: *in.UpdatedParsed,
		Created: *in.PublishedParsed,
		Content: in.Content,
	}
	if len(in.Enclosures) > 0 {
		e := in.Enclosures[0]
		item.Enclosure = &feeds.Enclosure{
			Url: e.URL,
			Length: e.Length,
			Type: e.Type,
		}
	}

	return item
}

// we don't trust that their GUIDs are actually globally unique,
// so we make our own by hashing the feed url and their guid and
// concatting the hashes.
func bundledGuid(feed_url string, in_guid string) string {
	in := in_guid
	if in == nil {
		in = "nil"
	}
	md5.New()
	return fmt.Sprintf("bundled-%s-%s", shortHash(feed_url), shortHash(in_guid))
}

func shortHash(in string) string {
	in := in
	if in == nil {
		// so we can at least have some hash, even if all the nil
		// ones are the same.
		in = "nil"
	}
	in := strings.TrimSpace(in)
	if len(in) == 0 {
		in = "nil"
	}

	in_b := []byte(in)
	hash := md5.Sum(in_b)
	hash_str := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(hash)
	
	return firstNOfString(hash_str, 7)
}

// return the first n runes of s
func firstNOfString(s string, n int) string {
    i := 0
    for j := range s {
        if i == n {
            return s[:j]
        }
        i++
    }
    return s
}
