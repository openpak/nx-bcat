package main

// Subscription delivery, moved from nx-baas news.go. The bcat module does not poll: a topic
// refreshes on an NPNS message {"type":"news","topic_id":…,"revision":"…"[,"wait_range":…]},
// which schedules a fetch at a random delay within wait_range (default 3600 s). Consoles
// subscribe topics over the Penne frontline, so the delivery itself is the push side's
// (nx-penne, or nx-baas while it serves Penne): POST /internal/penne/topic-messages.
//
// Live delivery only: a console that is offline refreshes on its next boot anyway, and a stale
// revision is worthless.

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/openpak/nx-common/internalkey"
	"github.com/openpak/nx-common/nxapi"
)

type topicPusher interface {
	TopicMessage(ctx context.Context, m nxapi.TopicMessage) (*nxapi.TopicResult, error)
}

type pusher struct {
	penne     topicPusher
	receiver  string // the Penne Meta routing token; bcat listens under "bcat" (ListenToByName)
	waitRange json.Number
}

// send delivers one topic's message. subscription names the Penne topic string when it differs
// from the News topic id; without it the topic id itself is matched (lower-cased).
func (p *pusher) send(topic, subscription string, revision json.Number) (*nxapi.TopicResult, error) {
	// bcat reads "$.revision" with its string getter and hashes it; a bare number is ignored
	// and nothing is fetched (23.0.1 bcat FUN_00206f50, proven on Harbor 2026-10-16).
	msg := map[string]any{"type": "news", "topic_id": topic, "revision": revision.String()}
	if p.waitRange != "" {
		msg["wait_range"] = p.waitRange
	}
	body, _ := json.Marshal(msg)
	if subscription == "" {
		subscription = topic
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := p.penne.TopicMessage(ctx, nxapi.TopicMessage{
		Topic: strings.ToLower(subscription), Name: "news", Receivers: p.receiver, Body: string(body)})
	if err != nil {
		log.Printf("%s push %s revision %s: %v", logPrefix, topic, revision, err)
		return nil, err
	}
	log.Printf("%s push %s revision %s -> topic %q: %d subscriber(s), %d connection(s)", logPrefix, topic, revision, subscription, res.Subscribers, res.Delivered)
	return res, nil
}

// pushAll is the tree's callback after a build changes topics.
func (p *pusher) pushAll(topics []string, revision int64) {
	rev := json.Number(itoa(revision))
	for _, t := range topics {
		p.send(t, "", rev)
	}
}

// handle is POST /internal/news/push, nx-baas's route and body unchanged, for anything that
// still pushes by hand.
func (p *pusher) handle(w http.ResponseWriter, r *http.Request) {
	if !internalkey.Strict(r) {
		apiErr(w, http.StatusUnauthorized, "internal")
		return
	}
	if r.Method != http.MethodPost {
		apiErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var in struct {
		TopicID      string      `json:"topic_id"`
		Revision     json.Number `json:"revision"`
		WaitRange    json.Number `json:"wait_range"`
		Subscription string      `json:"subscription"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.UseNumber()
	if err := dec.Decode(&in); err != nil || in.TopicID == "" || in.Revision == "" {
		apiErr(w, http.StatusBadRequest, "topic_id and revision required")
		return
	}
	q := *p
	if in.WaitRange != "" {
		q.waitRange = in.WaitRange
	}
	res, err := q.send(in.TopicID, in.Subscription, in.Revision)
	if err != nil {
		apiErr(w, http.StatusBadGateway, "push side unreachable")
		return
	}
	writeJSON(w, 200, map[string]any{"topic": in.TopicID, "subscribers": res.Subscribers, "delivered": res.Delivered})
}
