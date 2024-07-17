package main

import (
	"context"
	"crypto/md5"
	"encoding/base32"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/mmcdole/gofeed"

	"github.com/gorilla/feeds"
	"github.com/sethvargo/go-envconfig"
	"github.com/thejerf/suture/v4"
	"github.com/thejerf/sutureslog"
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

	Out_error_cooldown time.Duration `env:"OUTFEED_ERROR_COOLDOWN, default=24h`
}

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

	var config Config

	soup := suture.New("soupervisor", suture.Spec{
		EventHook: soup_logger.MustHook(),
	})

	ctx, cancel := context.WithCancel(context.Background())

	soup.ServeBackground(ctx)

	err := envconfig.Process(ctx, &config)
	if err != nil {
		log.Error("configuration error", "err", err)
		os.Exit(1)
	}

	bundler := NewBundler(
		log,
		soup,
		config,
	)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "Content-type: text/xml;charset=UTF-8")
		rss_str, ok := bundler.GetFeed()
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

type RssBundler struct {
	log    *slog.Logger
	config Config

	// get the current bundled rss feed as a string.
	get_feed chan string
}

type rssBundlerService struct {
	RssBundler
	cached_feed  string
	update_cache chan string

	// the service will use this to communicate with the bundleCreatorService
	bundle_creator bundleCreator
}

// this actually fetches the rss feeds and creates the bundled one. it's in a
// seperate service so the application can handle panics better (although
// hopefully that won't happen).
type bundleCreator struct {
	RssBundler
	// it will send the result on the channel you send it once it's done.
	Make_bundle chan chan string
}

// if certain errors happen when creating the bundle (like an input feed was
// down), we put an entry saying there was an error in our output bundled feed.
// Entries need to have a time and a uuid; this is how the user's rss client
// knows when it is a new entry versus one it has already got in a previous
// fetch of the feed. the errorEntryMetadataService generates and caches the
// time and uuid for error entries, so they aren't shown a new one every hour
// (or however often we recreate the bundled feed). it can invalidate the cache
// every n hours so that if errors continue happening, users will still see them
// without spamming them with them.
type errorEntryMetadataService struct {
	RssBundler
	metadata_cache map[string]errorEntryMetadata
}

type errorEntryMetadata struct {
	Entry_uuid string
	Entry_time time.Time
}

func NewBundler(
	l *slog.Logger,
	soup *suture.Supervisor,
	config Config,
) *RssBundler {

	e := RssBundler{
		log:      l.WithGroup("bundler"),
		config:   config,
		get_feed: make(chan string),
	}

	e_bundle_service := bundleCreator{
		RssBundler:  e,
		Make_bundle: make(chan chan string),
	}

	e_service := rssBundlerService{
		RssBundler:     e,
		cached_feed:    "",
		update_cache:   make(chan string),
		bundle_creator: e_bundle_service,
	}

	soup.Add(e_bundle_service)
	soup.Add(e_service)

	return &e
}

// gets the current cached rss feed as a string. returns true if the feed was
// gotten successfully, false if not.
func (e *RssBundler) GetFeed() (string, bool) {
	select {
	case feed := <-e.get_feed:
		return feed, true
	case <-time.After(time.Millisecond * 500):
		e.log.Error("GetFeed: failed to get feed")
		return "", false
	}
}

func (e rssBundlerService) Serve(ctx context.Context) error {
	// update the cache right away once the service starts.
	e.doUpdateCache()
	refetch := time.NewTicker(e.config.Fetch_interval)

	for {
		select {
		case <-refetch.C:
			e.doUpdateCache()
		case e.get_feed <- e.cached_feed:
		case f := <-e.update_cache:
			e.log.Debug("updated cached feed")
			e.cached_feed = f
		case <-ctx.Done():
			return nil
		}
	}
}

func (e bundleCreator) Serve(ctx context.Context) error {
	for {
		select {
		case response := <-e.Make_bundle:
			updateCache(
				ctx,
				e.log,
				e.config,
				response,
			)
		case <-ctx.Done():
			return nil
		}
	}
}

func (e *rssBundlerService) doUpdateCache() {
	e.bundle_creator.Make_bundle <- e.update_cache
}

func updateCache(
	ctx context.Context,
	log *slog.Logger,
	c Config,
	send_result_to chan string,
) {
	var out_items []feeds.Item

	// we fetch the feeds and append their items to `out_items`
	for _, url := range c.Feed_urls {
		// fetch the feed.
		feed, err := fetchFeed(
			ctx, log, url, c.Fetch_timeout,
		)

		// if there was an error, create an entry that we will put in
		// the resulting rss feed.
		if err != nil {
			log.Info("failed to fetch feed", "url", url, "err", err)
			out_items = append(out_items, failedToFetch(url))
			continue
		}

		// turn the feed into Items and append them to `out_items`
		out_i := feedToItems(*feed, url)
		out_items = append(out_items, out_i...)
	}

	// now we sort `out_items`
	sort.Sort(ByDate(out_items))

	// now we turn it into a feed.
	out_feed := itemsToFeed(out_items, c)

	rss, err := out_feed.ToRss()
	if err != nil {
		panic(err)
	}

	send_result_to <- rss
}

func itemsToFeed(items []feeds.Item, c Config) feeds.Feed {
	i_items := make([]*feeds.Item, 0, len(items))
	for _, item := range items {
		i_items = append(i_items, &item)
	}

	return feeds.Feed{
		Title: c.Out_title,
		Link: &feeds.Link{
			Href: c.Out_link,
		},
		Description: c.Out_description,
		Author: &feeds.Author{
			Name:  c.Out_author_name,
			Email: c.Out_author_email,
		},
		Created: time.Now(),
		Items:   i_items,
	}
}

// ByDate implements sort.Interface for []feeds.Item based on how long ago an
// item was published.
type ByDate []feeds.Item

var _ sort.Interface = (*ByDate)(nil)

func (a ByDate) Len() int {
	return len(a)
}

func (a ByDate) Swap(i int, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a ByDate) Less(i int, j int) bool {
	return a[i].Created.After(a[i].Created)
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
	if feed_title == "" && entry_title == "" {
		return ""
	}
	if feed_title == "" {
		return entry_title
	}
	if entry_title == "" {
		return feed_title
	}
	return fmt.Sprintf("%s | %s", feed_title, entry_title)
}

func feedToItems(in_feed gofeed.Feed, url string) []feeds.Item {
	var items []feeds.Item

	for _, fi := range in_feed.Items {
		new_item := inItemToOutItem(*fi, in_feed.Title, url)
		items = append(items, new_item)
	}

	return items
}

func inItemToOutItem(in gofeed.Item, feed_title string, feed_url string) feeds.Item {
	item := feeds.Item{
		Title:       entryTitle(feed_title, in.Title),
		Link:        &feeds.Link{Href: in.Link},
		Source:      &feeds.Link{Href: feed_url},
		Description: in.Description,
		Id:          bundledGuid(feed_url, in.GUID),
		Content:     in.Content,
	}

	if in.UpdatedParsed != nil {
		item.Updated = *in.UpdatedParsed
	}
	if in.PublishedParsed != nil {
		item.Created = *in.PublishedParsed
	} else {
		// TODO: we should probably handle this better.
		item.Created = time.Now()
	}

	if in.Author != nil {
		item.Author = &feeds.Author{Name: in.Author.Name, Email: in.Author.Email}
	}

	if len(in.Enclosures) > 0 {
		e := in.Enclosures[0]
		item.Enclosure = &feeds.Enclosure{
			Url:    e.URL,
			Length: e.Length,
			Type:   e.Type,
		}
	}

	return item
}

// we don't trust that their GUIDs are actually globally unique,
// so we make our own by hashing the feed url and their guid and
// concatting the hashes.
func bundledGuid(feed_url string, in_guid string) string {
	in := in_guid
	if in == "" {
		in = "nil"
	}
	md5.New()
	return fmt.Sprintf("bundled-%s-%s", shortHash(feed_url), shortHash(in_guid))
}

func shortHash(in string) string {
	if in == "" {
		// so we can at least have some hash, even if all the nil
		// ones are the same.
		in = "nil"
	}
	in = strings.TrimSpace(in)
	if len(in) == 0 {
		in = "nil"
	}

	in_b := []byte(in)
	hash := md5.Sum(in_b)
	hash_str := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(hash[:])

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
