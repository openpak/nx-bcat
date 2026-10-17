// SPDX-License-Identifier: AGPL-3.0-only
package newsformat

import (
	"bytes"
	"testing"
)

func TestUnsignedBCATDataIDKeepsAllBits(t *testing.T) {
	encoded, err := Encode(uint64(0xffffffffffffffff))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, []byte{0xcf, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}) {
		t.Fatalf("truncated unsigned data ID: %x", encoded)
	}
}

func mustEncode(t *testing.T, v any) []byte {
	t.Helper()
	b, err := Encode(v)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return b
}

func TestPackPrimitives(t *testing.T) {
	cases := []struct {
		in   any
		want []byte
	}{
		{false, []byte{0xc2}},
		{true, []byte{0xc3}},
		{int64(0), []byte{0x00}},
		{int64(127), []byte{0x7f}},
		{int64(128), []byte{0xcc, 0x80}},
		{int64(255), []byte{0xcc, 0xff}},
		{int64(256), []byte{0xcd, 0x01, 0x00}},
		{int64(65535), []byte{0xcd, 0xff, 0xff}},
		{int64(65536), []byte{0xce, 0x00, 0x01, 0x00, 0x00}},
		// Timestamp-sized values must stay uint32, matching the compact
		// widths the console's own payloads use.
		{int64(1789000000), []byte{0xce, 0x6a, 0xa1, 0xf9, 0x40}},
		{int64(-1), []byte{0xff}},
		{int64(-33), []byte{0xd0, 0xdf}},
		{"", []byte{0xa0}},
		{"abc", []byte{0xa3, 'a', 'b', 'c'}},
		{[]byte{}, []byte{0xc4, 0x00}},
		{[]byte{0x01, 0x02}, []byte{0xc4, 0x02, 0x01, 0x02}},
	}
	for _, tc := range cases {
		if got := mustEncode(t, tc.in); !bytes.Equal(got, tc.want) {
			t.Errorf("Encode(%v) = % x, want % x", tc.in, got, tc.want)
		}
	}
}

func TestPackContainers(t *testing.T) {
	if got := mustEncode(t, []any{}); !bytes.Equal(got, []byte{0x90}) {
		t.Errorf("empty array = % x", got)
	}
	if got := mustEncode(t, Map{}); !bytes.Equal(got, []byte{0x80}) {
		t.Errorf("empty map = % x", got)
	}
	got := mustEncode(t, Map{{"a", int64(1)}, {"b", "xy"}})
	want := []byte{0x82, 0xa1, 'a', 0x01, 0xa1, 'b', 0xa2, 'x', 'y'}
	if !bytes.Equal(got, want) {
		t.Errorf("map = % x, want % x", got, want)
	}
	// 17 entries must use map16, preserving insertion order.
	big := Map{}
	for i := 0; i < 17; i++ {
		big = append(big, Field{Key: string(rune('a' + i)), Value: int64(i)})
	}
	got = mustEncode(t, big)
	if got[0] != 0xde {
		t.Fatalf("17-entry map header = %x, want de (map16)", got[0])
	}
}

func TestPackRejectsUnknown(t *testing.T) {
	if _, err := Encode(1.5); err == nil {
		t.Fatal("float accepted")
	}
	if _, err := Encode(map[string]any{"a": 1}); err == nil {
		t.Fatal("plain Go map accepted")
	}
}

func TestCatalogRecordOrder(t *testing.T) {
	ch := Channel{TopicID: "t", Name: "n", Publisher: "p", Description: "d",
		Important: false, PublishingTime: 10, LastPostedAt: 20}
	want := []string{"topic_id", "name", "publisher", "description", "important",
		"publishing_time", "last_posted_at"}
	assertOrder(t, ch.record(), want)

	// No parser reads latest_news_urls; detail is the bare record unless
	// the channel has a shop button.
	assertOrder(t, Detail(ch), want)
	ch.ShopQuery = "dst_nsuid=70010000000001"
	detail := Detail(ch)
	assertOrder(t, detail, append(append([]string{}, want...), "shop_query"))
	if detail[len(detail)-1].Value != ch.ShopQuery {
		t.Fatal("shop_query value")
	}

	arch := Archives()
	assertOrder(t, arch, []string{"na_required", "data_list"})
	if arch[0].Value != false || len(arch[1].Value.([]any)) != 0 {
		t.Fatal("archives payload not the validated empty shape")
	}
}

func TestArticlePayloadOrder(t *testing.T) {
	a := Article{
		NewsID: 123, PublishedAt: 1000, ExpireAt: 2000,
		Language: "en-GB", SupportedLanguages: []string{"en-GB", "en-US"},
		TopicID: "t", TopicName: "T", TopicPublisher: "p", TopicDescription: "d",
		TopicImage: []byte{1}, Title: "hi", ListImage: []byte{2}, Footer: "f",
		BodyText: "b", BodyImage: []byte{3}, BodyImageHeight: 364,
	}
	p := a.Payload()
	want := []string{"version", "news_id", "published_at", "expire_at", "pickup_limit",
		"priority", "deletion_priority", "language", "supported_languages",
		"display_type", "topic_id", "no_photography", "surprise",
		"movie", "subject", "topic_name", "topic_image",
		"topic_publisher", "topic_description", "list_image", "footer", "body"}
	assertOrder(t, p, want)
	if got := p[1].Value.(int64); got != 123 {
		t.Fatalf("news_id = %d", got)
	}
	subject := p[14].Value.(Map)
	assertOrder(t, subject, []string{"caption", "text"})
	body := p[21].Value.(Map)
	assertOrder(t, body, []string{"text", "main_image_height", "main_image"})
	// qlaunch lists 50 < priority < 1000 only.
	if got := p[5].Value.(int64); got != 100 {
		t.Fatalf("priority = %d, want 100", got)
	}
	// No essential window: no key at all (pickup_limit 0 breaks qlaunch's
	// text parsers). With a window it sits before movie, both values set.
	withWindow := a
	withWindow.EssentialPickupLimit = 2000000000
	ep := withWindow.Payload()[13]
	if ep.Key != "essential_pickup" {
		t.Fatalf("field 13 = %q, want essential_pickup", ep.Key)
	}
	assertOrder(t, ep.Value.(Map), []string{"pickup_limit", "priority_after"})
	if v := ep.Value.(Map); v[0].Value.(int64) != 2000000000 || v[1].Value.(int64) != 100 {
		t.Fatal("essential_pickup values")
	}

	// more is appended only when set, with only the fields the author set.
	a.More = &More{Type: "game", ApplicationIDs: []uint64{0x0100abcd12340000}}
	p = a.Payload()
	assertOrder(t, p, append(append([]string{}, want...), "more"))
	more := p[len(p)-1].Value.(Map)
	assertOrder(t, more, []string{"type", "application_ids"})
	if more[1].Value.([]any)[0].(int64) != 0x0100abcd12340000 {
		t.Fatal("application id")
	}
	a.More = &More{Type: "browser", URL: "https://openpak.example/x"}
	assertOrder(t, a.Payload()[len(want)].Value.(Map), []string{"type", "url"})
	a.More = &More{Type: "shop", Query: "dst_nsuid=1"}
	assertOrder(t, a.Payload()[len(want)].Value.(Map), []string{"type", "query"})
	a.More = nil

	// Expired variant keeps identity and shrinks the window.
	e := a.ExpiredPayload()
	if got := e[1].Value.(int64); got != 123 {
		t.Fatalf("expired news_id = %d", got)
	}
	if got := e[2].Value.(int64); got != 1000-86400 {
		t.Fatalf("expired published_at = %d", got)
	}
	if got := e[3].Value.(int64); got != 999 {
		t.Fatalf("expired expire_at = %d", got)
	}
	if got := e[4].Value.(int64); got != 999 {
		t.Fatalf("expired pickup_limit = %d", got)
	}
}

func TestEncodeDeterminism(t *testing.T) {
	ch := Channel{TopicID: "t", Name: "n", Publisher: "p", Description: "d",
		PublishingTime: 1, LastPostedAt: 2}
	a := Article{NewsID: 5, PublishedAt: 3, ExpireAt: 4, Language: "en-GB",
		SupportedLanguages: []string{"en-GB"}, TopicID: "t", TopicName: "T",
		TopicPublisher: "p", TopicDescription: "d", TopicImage: []byte{9},
		Title: "x", ListImage: []byte{8}, Footer: "f", BodyText: "b",
		BodyImage: []byte{7}, BodyImageHeight: 364}
	first := mustEncode(t, Catalog([]Channel{ch}))
	for i := 0; i < 3; i++ {
		again := mustEncode(t, Catalog([]Channel{ch}))
		if !bytes.Equal(first, again) {
			t.Fatal("catalog encoding not deterministic")
		}
	}
	if !bytes.Equal(mustEncode(t, a.Payload()), mustEncode(t, a.Payload())) {
		t.Fatal("article encoding not deterministic")
	}
}

func TestListShape(t *testing.T) {
	e := ListEntry{NewsID: 7, PublishingTime: 1000, DeletionPriority: 100, DefaultLanguage: "en-GB",
		Languages: []ListLanguage{{Language: "en-GB", DataID: 42, URL: "https://h/x", Size: 400, Overwrite: true}}}
	l := List("openpak_news", InService, false, e)
	assertOrder(t, l, []string{"topic_id", "service_status", "na_required", "directories"})
	if l[1].Value != "in_service" || l[2].Value != false {
		t.Fatal("list header values")
	}
	dirs := l[3].Value.([]any)
	if len(dirs) != 1 {
		t.Fatal("want exactly one directory")
	}
	dir := dirs[0].(Map)
	assertOrder(t, dir, []string{"data_list"})
	entry := dir[0].Value.([]any)[0].(Map)
	assertOrder(t, entry, []string{"version", "news_id", "publishing_time",
		"deletion_priority", "default_language", "languages"})
	assertOrder(t, entry[0].Value.(Map), []string{"format", "semantics"})
	lang := entry[5].Value.([]any)[0].(Map)
	assertOrder(t, lang, []string{"language", "data_id", "url", "size", "overwrite"})
	if lang[4].Value != true || lang[3].Value.(int64) != 400 {
		t.Fatal("language values")
	}

	x := List("openpak_old", Expired, true)
	assertOrder(t, x, []string{"topic_id", "service_status", "na_required", "test_distribution", "directories"})
	if x[1].Value != "expired" || len(x[4].Value.([]any)[0].(Map)[0].Value.([]any)) != 0 {
		t.Fatal("expired list")
	}
	// Byte-level check of the header so an encoder change cannot reorder it.
	b := mustEncode(t, List("t", InService, false))
	want := []byte{0x84, 0xa8, 't', 'o', 'p', 'i', 'c', '_', 'i', 'd', 0xa1, 't'}
	if !bytes.HasPrefix(b, want) {
		t.Fatalf("list prefix % x", b[:len(want)])
	}
}

func TestTitleTopics(t *testing.T) {
	got, err := TitleTopics([]string{"openpak_games"})
	if err != nil || !bytes.Equal(mustEncode(t, got), append([]byte{0x91, 0xad}, "openpak_games"...)) {
		t.Fatalf("title topics = % x, %v", mustEncode(t, got), err)
	}
	empty, err := TitleTopics(nil)
	if err != nil || !bytes.Equal(mustEncode(t, empty), []byte{0x90}) {
		t.Fatal("empty title topics must be an empty array")
	}
	if _, err := TitleTopics(make([]string, 65)); err == nil {
		t.Fatal("65 topics accepted")
	}
}

func assertOrder(t *testing.T, m Map, want []string) {
	t.Helper()
	if len(m) != len(want) {
		t.Fatalf("map has %d fields, want %d", len(m), len(want))
	}
	for i, key := range want {
		if m[i].Key != key {
			t.Fatalf("field %d = %q, want %q", i, m[i].Key, key)
		}
	}
}
