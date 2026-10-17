// SPDX-License-Identifier: AGPL-3.0-only
// Package newsformat encodes platform-neutral News content into the
// MessagePack payload shapes the native Switch News reader accepts.
//
// The NORMAL article rendered in the v0.12 local test and native catalog
// decoding succeeded on 2026-09-09. Populated archives and summaries follow
// owner-local qlaunch parser analysis; hardware acceptance is a separate gate.
// Maps are ordered and integers use compact widths. The package
// holds no keys and no container logic; wrapping into signed/encrypted
// BCAT containers is internal/bcatcontainer's job.
package newsformat

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

// Field is one ordered map entry. The console reads maps by key, but every
// payload hardware has accepted used these exact orderings, so builders
// emit them deterministically.
type Field struct {
	Key   string
	Value any
}

// Map is an ordered MessagePack map.
type Map []Field

// ---------- encoder ----------

// Encode serialises v using only the types the News payloads need.
func Encode(v any) ([]byte, error) {
	var b bytes.Buffer
	if err := pack(&b, v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func writeBytes(w io.Writer, b []byte) error {
	n, err := w.Write(b)
	if err == nil && n != len(b) {
		return io.ErrShortWrite
	}
	return err
}

func lengthHeader(w io.Writer, n int, fixBase byte, fixLimit int, tag8, tag16, tag32 byte) error {
	if n < fixLimit {
		return writeBytes(w, []byte{fixBase | byte(n)})
	}
	if n <= 255 && tag8 != 0 {
		return writeBytes(w, []byte{tag8, byte(n)})
	}
	if n <= 65535 {
		b := []byte{tag16, 0, 0}
		binary.BigEndian.PutUint16(b[1:], uint16(n))
		return writeBytes(w, b)
	}
	if uint64(n) > uint64(^uint32(0)) {
		return fmt.Errorf("newsformat value too large")
	}
	b := []byte{tag32, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(b[1:], uint32(n))
	return writeBytes(w, b)
}

func pack(w io.Writer, v any) error {
	switch x := v.(type) {
	case bool:
		if x {
			return writeBytes(w, []byte{0xc3})
		}
		return writeBytes(w, []byte{0xc2})
	case int:
		return pack(w, int64(x))
	case uint64:
		if x <= uint64(^uint64(0)>>1) {
			return pack(w, int64(x))
		}
		b := make([]byte, 9)
		b[0] = 0xcf
		binary.BigEndian.PutUint64(b[1:], x)
		return writeBytes(w, b)
	case int64:
		if x >= -32 && x <= 127 {
			return writeBytes(w, []byte{byte(x)})
		}
		var b []byte
		switch {
		case x >= 0 && x <= 255:
			b = []byte{0xcc, byte(x)}
		case x >= 0 && x <= 65535:
			b = []byte{0xcd, 0, 0}
			binary.BigEndian.PutUint16(b[1:], uint16(x))
		case x >= 0 && x <= 4294967295:
			b = make([]byte, 5)
			b[0] = 0xce
			binary.BigEndian.PutUint32(b[1:], uint32(x))
		case x >= 0:
			b = make([]byte, 9)
			b[0] = 0xcf
			binary.BigEndian.PutUint64(b[1:], uint64(x))
		case x >= -128:
			b = []byte{0xd0, byte(x)}
		case x >= -32768:
			b = []byte{0xd1, 0, 0}
			binary.BigEndian.PutUint16(b[1:], uint16(x))
		case x >= -2147483648:
			b = make([]byte, 5)
			b[0] = 0xd2
			binary.BigEndian.PutUint32(b[1:], uint32(x))
		default:
			b = make([]byte, 9)
			b[0] = 0xd3
			binary.BigEndian.PutUint64(b[1:], uint64(x))
		}
		return writeBytes(w, b)
	case string:
		if err := lengthHeader(w, len(x), 0xa0, 32, 0xd9, 0xda, 0xdb); err != nil {
			return err
		}
		return writeBytes(w, []byte(x))
	case []byte:
		if err := lengthHeader(w, len(x), 0, 0, 0xc4, 0xc5, 0xc6); err != nil {
			return err
		}
		return writeBytes(w, x)
	case []any:
		if err := lengthHeader(w, len(x), 0x90, 16, 0, 0xdc, 0xdd); err != nil {
			return err
		}
		for _, child := range x {
			if err := pack(w, child); err != nil {
				return err
			}
		}
	case Map:
		if err := lengthHeader(w, len(x), 0x80, 16, 0, 0xde, 0xdf); err != nil {
			return err
		}
		for _, entry := range x {
			if err := pack(w, entry.Key); err != nil {
				return err
			}
			if err := pack(w, entry.Value); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unsupported newsformat type %T", v)
	}
	return nil
}

// ---------- payload builders ----------

// Channel is one catalog channel record.
type Channel struct {
	TopicID        string
	Name           string
	Publisher      string
	Description    string
	Important      bool
	PublishingTime int64 // UTC Unix seconds
	LastPostedAt   int64 // UTC Unix seconds
	// ShopQuery is the raw eShop query behind the channel page's shop
	// button (qlaunch passes it to the shop applet unchanged). Detail only;
	// omitted when empty.
	ShopQuery string
}

func (ch Channel) record() Map {
	return Map{
		{"topic_id", ch.TopicID},
		{"name", ch.Name},
		{"publisher", ch.Publisher},
		{"description", ch.Description},
		{"important", ch.Important},
		{"publishing_time", ch.PublishingTime},
		{"last_posted_at", ch.LastPostedAt},
	}
}

// Catalog encodes the topic catalog: an array of channel records.
func Catalog(channels []Channel) []any {
	out := make([]any, 0, len(channels))
	for _, ch := range channels {
		out = append(out, ch.record())
	}
	return out
}

// Detail encodes one channel's detail record: the catalog record plus the
// optional shop_query. latest_news_urls is not emitted: no 22.5.0 parser
// reads it (the earlier seeds sent it empty).
func Detail(ch Channel) Map {
	d := ch.record()
	if ch.ShopQuery != "" {
		d = append(d, Field{Key: "shop_query", Value: ch.ShopQuery})
	}
	return d
}

// Archives encodes the channel archive, optionally with populated entries.
func Archives(entries ...Map) Map {
	list := make([]any, 0, len(entries))
	for _, entry := range entries {
		list = append(list, entry)
	}
	return Map{
		{"na_required", false},
		{"data_list", list},
	}
}

// ArchiveEntry follows qlaunch 22.5's online_archives parser. Language must
// precede summary_url: the streaming parser selects the URL using that value.
// Serial conditions are omitted: these original articles target all consoles.
func ArchiveEntry(id int64, language, summaryURL string) Map {
	return Map{
		{"news_id", id},
		{"version", Map{{"format", int64(1)}, {"semantics", int64(1)}}},
		{"default_language", language},
		{"languages", []any{Map{{"language", language}, {"summary_url", summaryURL}}}},
	}
}

// ServiceStatus values the bcat list parser accepts; anything else fails
// the whole list (0x67d). "expired" retires the topic on the console.
const (
	InService = "in_service"
	Expired   = "expired"
)

// ListLanguage is one language edition of a list entry. The module picks
// the entry whose language matches the console, else the one matching
// default_language, then downloads URL and expects exactly Size bytes.
type ListLanguage struct {
	Language  string
	DataID    int64
	URL       string // https, at most 256 bytes
	Size      int64  // exact byte length of what URL serves (the container)
	Overwrite bool   // replace an already received news_id
}

// ListEntry is one data_list entry of the bcat-list response.
type ListEntry struct {
	NewsID           int64
	PublishingTime   int64
	DeletionPriority int64
	DefaultLanguage  string
	Languages        []ListLanguage
}

func (e ListEntry) encode() Map {
	langs := make([]any, 0, len(e.Languages))
	for _, l := range e.Languages {
		// language first: the streaming parser only reads data_id, url,
		// size and overwrite of an edition it has already selected.
		langs = append(langs, Map{
			{"language", l.Language},
			{"data_id", l.DataID},
			{"url", l.URL},
			{"size", l.Size},
			{"overwrite", l.Overwrite},
		})
	}
	return Map{
		{"version", Map{{"format", int64(1)}, {"semantics", int64(1)}}},
		{"news_id", e.NewsID},
		{"publishing_time", e.PublishingTime},
		{"deletion_priority", e.DeletionPriority},
		{"default_language", e.DefaultLanguage},
		{"languages", langs},
	}
}

// List encodes the bcat-list response for one topic
// (/api/nx/v1/list/<topic>). topic_id, service_status and na_required must
// precede the directories: the parser rejects a data_list it reaches
// before all three. test_distribution is emitted only when set. The
// directory object carries data_list alone; no other directory field was
// recovered from the parser.
func List(topicID, status string, testDistribution bool, entries ...ListEntry) Map {
	list := make([]any, 0, len(entries))
	for _, e := range entries {
		list = append(list, e.encode())
	}
	m := Map{
		{"topic_id", topicID},
		{"service_status", status},
		{"na_required", false},
	}
	if testDistribution {
		m = append(m, Field{"test_distribution", true})
	}
	return append(m, Field{"directories", []any{Map{{"data_list", list}}}})
}

// MaxTitleTopics bounds the auto-subscription answer.
const MaxTitleTopics = 64

// TitleTopics encodes /api/nx/v1/titles/<tid>/topics: a bare array of topic
// id strings the console subscribes to when the title launches.
func TitleTopics(topics []string) ([]any, error) {
	if len(topics) > MaxTitleTopics {
		return nil, fmt.Errorf("%d topics for one title, max %d", len(topics), MaxTitleTopics)
	}
	return stringsToAny(topics), nil
}

// Summary is the separate card payload fetched through summary_url. Its url
// points to the full article; both responses use signed BCAT containers.
func (a Article) Summary(articleURL string) Map {
	return Map{
		{"no_photography", int64(0)},
		{"subject", Map{{"caption", int64(1)}, {"text", a.Title}}},
		{"movie", int64(0)},
		{"icon_image", a.TopicImage},
		{"list_image", a.ListImage},
		{"url", articleURL},
	}
}

// Article describes one NORMAL article in the locally proven layout.
type Article struct {
	NewsID      int64
	PublishedAt int64 // UTC Unix seconds
	ExpireAt    int64 // UTC Unix seconds; also written to pickup_limit

	Language           string
	SupportedLanguages []string

	TopicID          string
	TopicName        string
	TopicPublisher   string
	TopicDescription string
	TopicImage       []byte // 70x70 baseline JFIF JPEG

	Title     string
	ListImage []byte // 358x201 baseline JFIF JPEG
	Footer    string

	BodyText        string
	BodyImage       []byte // 800x364 baseline JFIF JPEG
	BodyImageHeight int64

	More *More // optional link button under the article

	// Optional record windows; zero values keep the defaults every seed
	// has sent: priority Priority, pickup_limit = expire_at, no essential
	// pickup window (the essential_pickup key is omitted).
	Priority               int64 // 0 = Priority
	PickupLimit            int64 // UTC Unix seconds; 0 = ExpireAt
	EssentialPickupLimit   int64 // UTC Unix seconds; 0 = none
	EssentialPriorityAfter int64 // 0 = the record priority
}

// More is the article's link button. Type is "game", "shop" or "browser";
// which of the other fields each type reads is inferred, not traced, so
// the builder emits exactly the fields the author set.
type More struct {
	Type           string
	ApplicationIDs []uint64 // title ids, e.g. 0x0100abcd12340000
	Query          string   // raw eShop query (shop)
	URL            string   // https page (browser)
}

func (m *More) encode() Map {
	out := Map{{"type", m.Type}}
	if len(m.ApplicationIDs) > 0 {
		ids := make([]any, 0, len(m.ApplicationIDs))
		for _, id := range m.ApplicationIDs {
			ids = append(ids, int64(id))
		}
		out = append(out, Field{"application_ids", ids})
	}
	if m.Query != "" {
		out = append(out, Field{"query", m.Query})
	}
	if m.URL != "" {
		out = append(out, Field{"url", m.URL})
	}
	return out
}

// Priority is the record priority of a normal article. qlaunch's main
// list shows priority>50 AND priority<1000 (1000..1998 is a second band),
// so 50 — what the seeds sent until 2026-09-23 — stored but never listed.
const Priority = 100

// DeletionPriority is shared by the record and its list entry.
const DeletionPriority = 100

// Payload encodes the live article.
func (a Article) Payload() Map {
	pickup := a.PickupLimit
	if pickup == 0 {
		pickup = a.ExpireAt
	}
	return a.payload(a.PublishedAt, a.ExpireAt, pickup, a.EssentialPickupLimit)
}

// ExpiredPayload encodes the retirement variant: the same news_id with a
// past validity window, used to replace a delivered article when it is
// retired. Expiry cleanup is console-scheduled, not an immediate delete.
func (a Article) ExpiredPayload() Map {
	return a.payload(a.PublishedAt-86400, a.PublishedAt-1, a.PublishedAt-1, 0)
}

func (a Article) payload(published, expire, pickup, essential int64) Map {
	priority := a.Priority
	if priority == 0 {
		priority = Priority
	}
	after := a.EssentialPriorityAfter
	if after == 0 {
		after = priority
	}
	m := Map{
		{"version", Map{{"format", int64(1)}, {"semantics", int64(1)}}},
		{"news_id", a.NewsID},
		{"published_at", published},
		{"expire_at", expire},
		{"pickup_limit", pickup},
		{"priority", priority},
		{"deletion_priority", int64(DeletionPriority)},
		{"language", a.Language},
		{"supported_languages", stringsToAny(a.SupportedLanguages)},
		{"display_type", "NORMAL"},
		{"topic_id", a.TopicID},
		{"no_photography", int64(0)},
		{"surprise", int64(0)},
	}
	// The record parser reads essential_pickup as a nested map; the flat
	// essential_pickup_limit the seeds used to send was ignored. With no
	// essential window the key is left out: qlaunch 22.5.0's handler
	// (0x090867c0) fails unless both values are non-zero, and that aborts
	// its article parsers before subject, so pickup_limit 0 showed every
	// tile and article without text.
	if essential != 0 {
		m = append(m, Field{"essential_pickup", Map{{"pickup_limit", essential}, {"priority_after", after}}})
	}
	m = append(m, Map{
		{"movie", int64(0)},
		{"subject", Map{{"caption", int64(1)}, {"text", a.Title}}},
		{"topic_name", a.TopicName},
		{"topic_image", a.TopicImage},
		{"topic_publisher", a.TopicPublisher},
		{"topic_description", a.TopicDescription},
		{"list_image", a.ListImage},
		{"footer", Map{{"text", a.Footer}}},
		{"body", Map{
			{"text", a.BodyText},
			{"main_image_height", a.BodyImageHeight},
			{"main_image", a.BodyImage},
		}},
	}...)
	if a.More != nil {
		m = append(m, Field{"more", a.More.encode()})
	}
	return m
}

func stringsToAny(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}
