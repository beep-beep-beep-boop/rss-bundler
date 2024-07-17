package entry_timeout

import (
	"context"
	"log/slog"
	"time"

	"github.com/beep-beep-beep-boop/rss-bundler/internal/common"
	"github.com/google/uuid"
)

// if certain errors happen when creating the bundle (like an input feed was
// down), we put an entry saying there was an error in our output bundled feed.
// Entries need to have a time and a uuid; this is how the user's rss client
// knows when it is a new entry versus one it has already got in a previous
// fetch of the feed. the errorEntryMetadataService generates and caches the
// time and uuid for error entries, so they aren't shown a new one every hour
// (or however often we recreate the bundled feed). it can invalidate the cache
// every n hours so that if errors continue happening, users will still see them
// without spamming them with them.
type EntryTimeout struct {
	log    *slog.Logger
	config common.Config

	get_entry chan entryRequest

	metadata_cache map[string]EntryMetadata
}

type EntryMetadata struct {
	Entry_uuid string
	Entry_time time.Time
}

type entryRequest struct {
	key            string
	send_result_to chan EntryMetadata
}

func New(
	log *slog.Logger,
	config common.Config,
) *EntryTimeout {

	return &EntryTimeout{
		log:       log,
		config:    config,
		get_entry: make(chan entryRequest),
	}
}

func (e EntryTimeout) Serve(ctx context.Context) error {
	e.metadata_cache = make(map[string]EntryMetadata)

	for {
		select {
		case req := <-e.get_entry:
			e.doGetEntry(req)
		case <-ctx.Done():
			return nil
		}
	}
}

func (e *EntryTimeout) Get(key string) (EntryMetadata, error) {
	request := entryRequest{
		key:            key,
		send_result_to: make(chan EntryMetadata),
	}
	select {
	case e.get_entry <- request:
	case <-time.After(time.Second * 5):
		e.log.Error("getting entry metadata timed out (send step)", "key", key)
		return EntryMetadata{}, common.ErrTimeout
	}

	select {
	case response := <-request.send_result_to:
		return response, nil
	case <-time.After(time.Second * 5):
		e.log.Error("getting entry metadata timed out (recieve step)", "key", key)
		return EntryMetadata{}, common.ErrTimeout
	}
}

// gets the current cached rss feed as a string. returns true if the feed was
// gotten successfully, false if not.
func (e *EntryTimeout) doGetEntry(req entryRequest) {
	entry := e.getEntry(req.key)

	select {
	case req.send_result_to <- entry:
	case <-time.After(time.Second * 5):
		panic("entry_timeout: no one recieved on channel")
	}
}

func (e *EntryTimeout) getEntry(key string) EntryMetadata {
	entry, ok := e.metadata_cache[key]
	if !ok {
		// if there isn't an entry, make one.
		new_entry := makeEntry()
		e.metadata_cache[key] = new_entry
		e.log.Debug("made new entry", "new_entry", new_entry)
		return new_entry
	}

	// we should invalidate the current entry and make a new one once the
	// cooldown time has passed after the time the entry was created.
	cutoff := entry.Entry_time.Add(e.config.Out_error_cooldown)

	// if the current time is past the cutoff time, invalidate it
	// and make a newe one.
	if time.Now().After(cutoff) {
		new_entry := makeEntry()
		e.log.Debug("recreated entry due to being past cutoff", "new_entry", new_entry)
		e.metadata_cache[key] = new_entry
		return new_entry
	}

	// it's in the cache window still, so we should just return it.
	return entry
}

// makes an entry with a random uuid and the current time as its time.
func makeEntry() EntryMetadata {
	_id, err := uuid.NewRandom()
	if err != nil {
		panic(err)
	}

	id := _id.String()

	return EntryMetadata{
		Entry_uuid: id,
		Entry_time: time.Now(),
	}
}
