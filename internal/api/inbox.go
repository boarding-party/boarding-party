package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/boarding-party/boardingparty/internal/inbox"
	"github.com/boarding-party/boardingparty/internal/store"
)

const checkTimeout = 15 * time.Second

func jsonError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// mailer returns the inbox service, or answers 404 when there is none.
func (s *server) mailer(w http.ResponseWriter) *inbox.Service {
	if s.mail == nil {
		jsonError(w, http.StatusNotFound, "inbox not available")
		return nil
	}
	return s.mail
}

type mailboxView struct {
	Host        string       `json:"host"`
	Port        int          `json:"port"`
	Username    string       `json:"username"`
	PasswordSet bool         `json:"passwordSet"`
	Status      inbox.Status `json:"status"`
}

// view never carries the password.
func view(c inbox.Config, saved bool, st inbox.Status) mailboxView {
	v := mailboxView{Status: st}
	if saved {
		v.Host, v.Port, v.Username, v.PasswordSet = c.Host, c.Port, c.Username, c.Password != ""
	}
	return v
}

func (s *server) getMailbox(w http.ResponseWriter, r *http.Request) {
	m := s.mailer(w)
	if m == nil {
		return
	}
	c, ok, err := m.Secrets.Load(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view(c, ok, m.Status()))
}

func (s *server) putMailbox(w http.ResponseWriter, r *http.Request) {
	m := s.mailer(w)
	if m == nil {
		return
	}
	if !mediaTypeIs(r, jsonType) {
		jsonError(w, http.StatusUnsupportedMediaType, "content type must be application/json")
		return
	}
	var c inbox.Config
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&c); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if c.Password == "" {
		old, ok, err := m.Secrets.Load(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		if !ok || old.Password == "" {
			jsonError(w, http.StatusBadRequest, "imap password is required")
			return
		}
		c.Password = old.Password
	}
	if err := c.Validate(); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), checkTimeout)
	defer cancel()
	if err := m.Check(ctx, c); err != nil {
		jsonError(w, http.StatusBadRequest, "Could not sign in: "+err.Error())
		return
	}
	if err := m.Secrets.Save(r.Context(), c); err != nil {
		fail(w, err)
		return
	}
	m.Poke()
	writeJSON(w, http.StatusOK, view(c, true, m.Status()))
}

func (s *server) deleteMailbox(w http.ResponseWriter, r *http.Request) {
	m := s.mailer(w)
	if m == nil {
		return
	}
	if err := m.Secrets.Delete(r.Context()); err != nil {
		fail(w, err)
		return
	}
	m.Poke()
	w.WriteHeader(http.StatusNoContent)
}

type messageSummary struct {
	ID       string    `json:"id"`
	Persona  string    `json:"persona"`
	From     string    `json:"from"`
	Subject  string    `json:"subject"`
	Received time.Time `json:"received"`
	Filtered string    `json:"filtered"`
}

type messageFull struct {
	messageSummary
	To    []string `json:"to"`
	Text  string   `json:"text"`
	Links []string `json:"links"`
}

func summary(m store.Message) messageSummary {
	return messageSummary{ID: m.ID, Persona: m.Persona, From: m.From, Subject: m.Subject, Received: m.Received, Filtered: m.Filtered}
}

func (s *server) listMessages(w http.ResponseWriter, r *http.Request) {
	m := s.mailer(w)
	if m == nil {
		return
	}
	q := r.URL.Query()
	mq := store.MessageQuery{Persona: q.Get("persona"), Filtered: q.Get("filtered") == "1"}
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 {
			jsonError(w, http.StatusBadRequest, "limit must be a positive number")
			return
		}
		mq.Limit = n
	}
	msgs, err := m.Store.Messages(mq)
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]messageSummary, 0, len(msgs))
	for _, x := range msgs {
		out = append(out, summary(x))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getMessage(w http.ResponseWriter, r *http.Request) {
	m := s.mailer(w)
	if m == nil {
		return
	}
	x, err := m.Store.Message(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	f := messageFull{messageSummary: summary(x), To: x.To, Text: x.Text, Links: x.Links}
	if f.To == nil {
		f.To = []string{}
	}
	if f.Links == nil {
		f.Links = []string{}
	}
	writeJSON(w, http.StatusOK, f)
}
