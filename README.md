# rss-bundler

rssbundler fetches multiple RSS feeds, combines them into one feed, and serves it via http.

to start, you need to set the `FEED_URLS` environment variable.

### Example

```
make
export FEED_URLS="https://website.com/feed1.xml, https://website.com/feed2.xml"
./rssbundler
```

## Configuration example

```bash
# the list of feeds to bundle into one feed.
# required.
FEED_URLS="https://example.com/feed1, https://example.com/feed2"

# the address the server should listen on.
# optional.
LISTEN_ADDR=":3000"

# how often to fetch all of the feeds and update the bundled one.
# optional.
FETCH_INTERVAL=1h

# the timeout when fetching a single feed, before it skips it.
# optional.
FETCH_TIMEOUT=1m

# the title of the output feed.
# optional.
OUTFEED_TITLE=bundle

# the link of the output feed, for the feed's metadata.
# optional. (defaults to empty).
OUTFEED_LINK="https://example.com/bundle"

# the description of the output feed.
# optional.
OUTFEED_DESCRIPTION="this is a bundle feed, which contains the entries from multiple rss feeds."

# the author name for the output feed.
# optional.
OUTFEED_AUTHOR="bundler robot"

# the author email for the output feed.
# optional. (defaults to empty).
OUTFEED_AUTHOR_EMAIL="example@example.com"

# when a feed fails to fetch or parse, we create an entry in the output rss feed
# indicating that that feed had an error. this is how often to re-post that
# entry, if the feed continues to fail. (restarting the program will also
# re-post the entry, since it does not save any state.)
OUTFEED_ERROR_COOLDOWN=24h

# whether to do debug logging.
DEBUG=false
```