package inbox

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/store"
)

// Config is how the director reaches the persona mailbox. The connection is always implicit TLS.
type Config struct {
	Host     string `json:"host"`
	Port     int    `json:"port"` // 0 means 993
	Username string `json:"username"`
	Password string `json:"password"`
}

// DefaultPort is the implicit-TLS IMAP port.
const DefaultPort = 993

// Validate reports the first problem with c. It never mentions the password.
func (c Config) Validate() error {
	switch {
	case c.Host == "":
		return errors.New("imap host is required")
	case strings.Contains(c.Host, "://") || strings.ContainsAny(c.Host, "/ \t\r\n"):
		return errors.New("imap host must be a bare host name, without scheme, path or spaces")
	case c.Port < 0 || c.Port > 65535:
		return errors.New("imap port must be between 1 and 65535")
	case c.Username == "":
		return errors.New("imap username is required")
	case c.Password == "":
		return errors.New("imap password is required")
	}
	return nil
}

// key identifies the mailbox account for deduping stored mail.
func (c Config) key() string {
	return strings.ToLower(c.Host) + "|" + strings.ToLower(c.Username)
}

func (c Config) addr() string {
	port := c.Port
	if port == 0 {
		port = DefaultPort
	}
	return net.JoinHostPort(c.Host, strconv.Itoa(port))
}

// Status is the poller's last outcome.
type Status struct {
	State   string    `json:"state"` // "off", "ok", "error"
	Error   string    `json:"error,omitempty"`
	Checked time.Time `json:"checked,omitzero"`
}

// Secrets holds the mailbox Config outside the database.
type Secrets interface {
	Load(ctx context.Context) (Config, bool, error)
	Save(ctx context.Context, c Config) error
	Delete(ctx context.Context) error
}

// Limits and reasons for messages the poller stores without parsing.
const (
	MaxMessage = 1 << 20

	FilteredTooLarge   = "too large"
	FilteredUnreadable = "could not read"

	dialTimeout    = 15 * time.Second
	commandTimeout = 2 * time.Minute
)

// scrub removes the password from err's text, in case a server or library echoes it.
func scrub(err error, password string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if password == "" || !strings.Contains(msg, password) {
		return err
	}
	return errors.New(strings.ReplaceAll(msg, password, "[redacted]"))
}

// connect dials with implicit TLS and logs in. The caller must Logout or Terminate.
func (s *Service) connect(ctx context.Context, c Config) (*client.Client, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	var cfg *tls.Config
	if s.TLS != nil {
		cfg = s.TLS.Clone()
	} else {
		cfg = &tls.Config{}
	}
	if cfg.ServerName == "" {
		cfg.ServerName = c.Host
	}
	if cfg.MinVersion < tls.VersionTLS12 {
		cfg.MinVersion = tls.VersionTLS12
	}
	d := &net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "tcp", c.addr())
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", c.addr(), err)
	}
	// Closing the connection on cancel unblocks the handshake, greeting and login reads.
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	tc := tls.Client(conn, cfg)
	var cl *client.Client
	if err = tc.HandshakeContext(ctx); err == nil {
		cl, err = client.New(tc)
	}
	if err != nil {
		stop()
		conn.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("connect to %s: %w", c.addr(), err)
	}
	cl.Timeout = commandTimeout
	if err := cl.Login(c.Username, c.Password); err != nil {
		stop()
		cl.Terminate()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("login as %s: %w", c.Username, err)
	}
	if !stop() { // cancelled during login: the connection is already closed
		cl.Terminate()
		return nil, ctx.Err()
	}
	return cl, nil
}

// poll runs one read-only pass over INBOX: it never sets flags, moves or deletes.
func (s *Service) poll(ctx context.Context, c Config) error {
	cl, err := s.connect(ctx, c)
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { cl.Terminate() })
	defer stop()
	if err := s.fetchNew(ctx, cl, c.key()); err != nil {
		cl.Terminate()
		return err
	}
	cl.Logout()
	return nil
}

type meta struct {
	uid  uint32
	size uint32
	env  *imap.Envelope
}

func (s *Service) fetchNew(ctx context.Context, cl *client.Client, mailbox string) error {
	mb, err := cl.Select("INBOX", true) // EXAMINE
	if err != nil {
		return fmt.Errorf("examine INBOX: %w", err)
	}
	validity, next := mb.UidValidity, mb.UidNext
	if next == 0 {
		if next, err = uidNext(cl, mb); err != nil {
			return err
		}
	}
	cv, last, ok, err := s.Store.MailCursor()
	if err != nil {
		return err
	}
	if !ok || cv != validity {
		// First connect or a rebuilt mailbox: start from here, import nothing.
		return s.Store.SetMailCursor(validity, next-1)
	}
	if next <= last+1 {
		return nil
	}

	seq := new(imap.SeqSet)
	seq.AddRange(last+1, 0) // last+1:*
	var metas []meta
	err = uidFetch(cl, seq, []imap.FetchItem{imap.FetchUid, imap.FetchRFC822Size, imap.FetchEnvelope}, func(m *imap.Message) {
		if m.Uid > last { // "n:*" always includes the newest message, even when its UID < n
			metas = append(metas, meta{uid: m.Uid, size: m.Size, env: m.Envelope})
		}
	})
	if err != nil {
		return fmt.Errorf("fetch new mail: %w", err)
	}
	slices.SortFunc(metas, func(a, b meta) int { return int(a.uid) - int(b.uid) })

	p, err := s.Store.Project()
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	for _, mt := range metas {
		if err := ctx.Err(); err != nil {
			return err
		}
		m, err := s.readOne(cl, mt, p)
		if err != nil {
			return err
		}
		m.UIDValidity, m.Mailbox = validity, mailbox
		added, err := s.Store.AddMessage(m)
		if err != nil {
			return err
		}
		if err := s.Store.SetMailCursor(validity, mt.uid); err != nil {
			return err
		}
		if added {
			s.Notify()
		}
	}
	return nil
}

// readOne turns one new message into a stored row. Only connection errors are returned;
// a message that is too large or unreadable becomes a filtered stub so it cannot stall the cursor.
func (s *Service) readOne(cl *client.Client, mt meta, p project.Project) (store.Message, error) {
	if mt.size > MaxMessage {
		return s.stub(mt, p, FilteredTooLarge), nil
	}
	seq := new(imap.SeqSet)
	seq.AddNum(mt.uid)
	section := &imap.BodySectionName{Peek: true}
	var raw []byte
	var readErr error
	err := uidFetch(cl, seq, []imap.FetchItem{imap.FetchUid, section.FetchItem()}, func(m *imap.Message) {
		if m.Uid != mt.uid {
			return
		}
		if lit := m.GetBody(section); lit != nil {
			raw, readErr = io.ReadAll(io.LimitReader(lit, MaxMessage+1))
		}
	})
	if err != nil {
		return store.Message{}, fmt.Errorf("fetch message %d: %w", mt.uid, err)
	}
	if raw == nil || readErr != nil || len(raw) > MaxMessage {
		return s.stub(mt, p, FilteredUnreadable), nil
	}
	im, err := Parse(raw)
	if err != nil {
		return s.stub(mt, p, FilteredUnreadable), nil
	}
	im.Persona, im.Filtered = Route(im, p)
	return s.toStore(im, mt.uid), nil
}

// stub records a message the poller could not parse, using what the envelope says.
func (s *Service) stub(mt meta, p project.Project, filtered string) store.Message {
	im := Message{}
	if e := mt.env; e != nil {
		im.Subject = e.Subject
		im.Received = e.Date
		if len(e.From) > 0 {
			im.From = strings.ToLower(e.From[0].Address())
		}
		for _, a := range append(slices.Clone(e.To), e.Cc...) {
			if a != nil && a.Address() != "" {
				im.To = append(im.To, strings.ToLower(a.Address()))
			}
		}
	}
	im.Persona, _ = Route(im, p) // persona only, for the UI; the verdict is filtered
	im.Filtered = filtered
	return s.toStore(im, mt.uid)
}

func (s *Service) toStore(m Message, uid uint32) store.Message {
	now := s.now()
	received := m.Received
	if received.IsZero() {
		received = now
	}
	return store.Message{
		ID:       newID(),
		UID:      uid,
		Persona:  m.Persona,
		From:     m.From,
		To:       m.To,
		Subject:  m.Subject,
		Text:     m.Text,
		Links:    m.Links,
		Received: received,
		Stored:   now,
		Filtered: m.Filtered,
	}
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b) // never fails
	return hex.EncodeToString(b)
}

// uidFetch runs UID FETCH and calls f for each message, draining the channel.
func uidFetch(cl *client.Client, seq *imap.SeqSet, items []imap.FetchItem, f func(*imap.Message)) error {
	ch := make(chan *imap.Message, 16)
	done := make(chan error, 1)
	go func() { done <- cl.UidFetch(seq, items, ch) }()
	for m := range ch {
		f(m)
	}
	return <-done
}

// uidNext works out UIDNEXT for servers that do not report it on EXAMINE.
func uidNext(cl *client.Client, mb *imap.MailboxStatus) (uint32, error) {
	if mb.Messages == 0 {
		return 1, nil
	}
	seq := new(imap.SeqSet)
	seq.AddNum(mb.Messages)
	var max uint32
	ch := make(chan *imap.Message, 1)
	done := make(chan error, 1)
	go func() { done <- cl.Fetch(seq, []imap.FetchItem{imap.FetchUid}, ch) }()
	for m := range ch {
		max = m.Uid
	}
	if err := <-done; err != nil {
		return 0, fmt.Errorf("find newest message: %w", err)
	}
	return max + 1, nil
}
