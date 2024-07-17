package bundler

import (
	"context"
	"crypto/md5"
	"encoding/base32"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/beep-beep-beep-boop/rss-bundler/internal/common"
	"github.com/beep-beep-beep-boop/rss-bundler/internal/entry_timeout"
	"github.com/gorilla/feeds"
	"github.com/mmcdole/gofeed"
)

// this actually fetches the rss feeds and creates the bundled one. it's in a
// seperate service so the application can handle panics better (although
// hopefully that won't happen).
type Bundler struct {
	log           *slog.Logger
	conf          common.Config
	entry_timeout *entry_timeout.EntryTimeout

	// it will send the result on the channel you send it once it's done.
	make_bundle chan chan string
}

func New(
	log *slog.Logger,
	config common.Config,
	e_timeout *entry_timeout.EntryTimeout,
) *Bundler {

	return &Bundler{
		log:           log,
		conf:          config,
		entry_timeout: e_timeout,
		make_bundle:   make(chan chan string, 5),
	}
}

func (e Bundler) Serve(ctx context.Context) error {
	for {
		select {
		case response := <-e.make_bundle:
			e.updateCache(
				ctx,
				response,
			)
		case <-ctx.Done():
			return nil
		}
	}
}

func (e *Bundler) MakeBundle(output_to chan string) {
	e.make_bundle <- output_to
}

func (e *Bundler) updateCache(ctx context.Context, send_result_to chan string) {
	var out_items []feeds.Item

	start_all := time.Now()
	e.log.Debug("fetching feeds...")

	// we fetch the feeds and append their items to `out_items`
	for _, url := range e.conf.Feed_urls {
		// fetch the feed.
		feed, err := e.fetchFeed(ctx, url)

		// if there was an error, create an entry that we will put in
		// the resulting rss feed.
		if err != nil {
			e.log.Info("failed to fetch feed", "url", url, "err", err)
			err_item, ok := e.failedToFetch(url)
			if ok {
				out_items = append(out_items, err_item)
			}
			continue
		}

		// turn the feed into Items and append them to `out_items`
		out_i := feedToItems(*feed, url)
		out_items = append(out_items, out_i...)
	}

	// now we sort `out_items`
	sort.Sort(ByDate(out_items))

	// now we turn it into a feed.
	out_feed := e.itemsToFeed(out_items)

	rss, err := out_feed.ToRss()
	if err != nil {
		panic(err)
	}

	e.log.Info("fetched all feeds", "took", time.Since(start_all))

	select {
	case send_result_to <- rss:
	case <-time.After(time.Minute * 1):
		e.log.Error("failed to send result to bundle requester")
	}
}

func (e *Bundler) itemsToFeed(items []feeds.Item) feeds.Feed {
	i_items := make([]*feeds.Item, 0, len(items))
	for _, item := range items {
		i_items = append(i_items, &item)
	}

	c := e.conf

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

func (e *Bundler) fetchFeed(
	ctx context.Context,
	url string,
) (*gofeed.Feed, error) {

	ctx, cancel := context.WithTimeout(ctx, e.conf.Fetch_timeout)
	defer cancel()

	e.log.Debug("fetching feed", "url", url)
	start := time.Now()

	fp := gofeed.NewParser()
	f, err := fp.ParseURLWithContext(url, ctx)

	l := e.log
	// only log `err` if there was one.
	if err != nil {
		l = l.With("err", err)
	}
	l.Info("finished fetching feed", "url", url, "took", time.Since(start))

	return f, err
}

func (e *Bundler) failedToFetch(url string) (feeds.Item, bool) {
	metadata, err := e.entry_timeout.Get(url)
	if err != nil {
		return feeds.Item{}, false
	}

	return feeds.Item{
		Title:   entryTitle("bundler errors", "failed to fetch feed :("),
		Content: fmt.Sprintf("url: %s", url),
		Created: metadata.Entry_time,
		Id:      metadata.Entry_uuid,
	}, true
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
