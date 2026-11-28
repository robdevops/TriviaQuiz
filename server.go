package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	mrand "math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/robdevops/TriviaQuiz/internal/state"
	"github.com/robdevops/signalbot/addon"
)

// server is the quiz: the chats' quizzes, their scores and the questions
// asked lately. srv speaks to the bot, and holds the outbox it reads.
type server struct {
	cfg   config
	clock clock
	log   *log.Logger
	doc   *state.Doc
	srv   *addon.Server
	slot  chan struct{} // one quiz written at a time, as the bot takes one sampling request at a time

	randMu sync.Mutex
	rand   *mrand.Rand

	mu      sync.Mutex
	quizzes map[string]*quiz   // by chat
	timers  map[string]stopper // each quiz's, by chat
	asks    map[string]stopper // each quiz's next question, posted after the gap, by chat
	warns   map[string]stopper // each quiz's ⏰ on its open question, warnBefore its deadline, by chat
	gap     time.Duration      // between a question's answer and the next question: gapTime, or 0 in most tests
	writing map[string]bool    // chats whose quiz the model is writing
	scores  map[string][]score // by chat, the last scoreDays days
	asked   map[string][]stamp // by chat, the questions of the last askedDays days
	prep    time.Duration      // how long a lobby's questions may take to write: prepMax, or less in tests
	preps   map[string]prepRun // the writing of a lobby's questions, by quiz
	prepGen int                // the last prepRun's gen
	life    context.Context    // the session's, while serve runs: preparation ends with it
	// stopping is set once the session is ending, before a sampling
	// request fails for it, so preparation leaves its quiz as it is.
	stopping bool

	prepWG sync.WaitGroup // the preparations running
	saveMu sync.Mutex     // one save at a time, so the last one written is the newest
}

// prepRun is one run writing a lobby's questions: its stop, and which run
// it is, so a run stopped and replaced, by a toggle back and forth, leaves
// the next alone.
type prepRun struct {
	stop context.CancelFunc
	gen  int
}

// quiz is one chat's quiz. Its answers are kept only as right or wrong:
// what anyone replied is never kept.
type quiz struct {
	ID        string            `json:"id"` // also the watch's
	Chat      string            `json:"chat"`
	Network   string            `json:"network,omitempty"` // the chat's network, as when the quiz began: "telegram" takes only some emoji as reactions
	Starter   string            `json:"starter"`           // who may end it
	Topic     string            `json:"topic"`
	Questions []question        `json:"questions"`
	Time      time.Duration     `json:"time"`    // each question's
	Started   time.Time         `json:"started"` // when question 1 opened
	End       time.Time         `json:"end"`     // when the last question is due to be revealed
	Current   int               `json:"current"` // the open question, from 0
	Post      string            `json:"post"`    // the outbox op that asked it, for the reveal to reply to
	Opened    time.Time         `json:"opened"`  // when it opened
	Deadline  time.Time         `json:"deadline"`
	Answers   map[string]answer `json:"answers"`             // the open question's, by player
	Players   []string          `json:"players"`             // who joined, or else who answered question 1: a question ends once they all have
	Lobby     bool              `json:"lobby,omitempty"`     // waiting for players to join by reacting to the intro
	Want      int               `json:"want,omitempty"`      // the questions asked for
	Preparing bool              `json:"preparing,omitempty"` // in its lobby, its questions are still being written
	Waiting   bool              `json:"waiting,omitempty"`   // its lobby is ready, and waits for its questions
	Joined    []string          `json:"joined,omitempty"`    // who has joined
	LastJoin  time.Time         `json:"lastJoin,omitempty"`  // when the last player joined: the quiz starts lobbyWait after
	Extra     time.Duration     `json:"extra,omitempty"`     // the starter's extra wait on top of lobbyWait, from /quiz wait or ⏱ +30s
	CallOff   time.Time         `json:"callOff,omitempty"`   // when the lobby gives up with fewer than minPlayers; with buttons, unless started
	// Buttons marks a lobby on a bot that shows buttons: one row, ✅ (Ready)
	// and the starter's toggle, ▶️ (Start) and ✖️ (Cancel), and no countdown:
	// only the starter starts it. BaseWant is what it opened with, the
	// toggle's first mode; the second doubles it, up to the limit.
	Buttons  bool           `json:"buttons,omitempty"`
	BaseWant int            `json:"baseWant,omitempty"`
	Seq      int            `json:"seq"`    // the answers recorded so far, to break ties
	Points   map[string]int `json:"points"` // by player
	Right    map[string]int `json:"right"`  // questions right, by player
}

// question is one question as played: its choices already shuffled.
type question struct {
	Q           string   `json:"q"`
	Kind        string   `json:"kind"` // "choice" or "text"
	Difficulty  string   `json:"difficulty"`
	Choices     []string `json:"choices,omitempty"`
	Answer      int      `json:"answer"`            // a choice question's, from 0
	Answers     []string `json:"answers,omitempty"` // a text question's accepted answers
	Explanation string   `json:"explanation"`
}

// answer is whether one player's first answer to the open question was
// right, and when it was sent.
type answer struct {
	Right bool      `json:"right"`
	At    time.Time `json:"at"`
	Seq   int       `json:"seq"`
}

// score is a player's points from one quiz.
type score struct {
	User   string    `json:"user"`
	Points int       `json:"points"`
	At     time.Time `json:"at"`
}

// stamp is a question asked in a chat: a hash of its normalised text, and
// when.
type stamp struct {
	Hash string    `json:"hash"`
	Fact string    `json:"fact,omitempty"` // the question and its answer, for the model to avoid
	At   time.Time `json:"at"`
}

// saved is quiz.json.
type saved struct {
	Quizzes []*quiz            `json:"quizzes"`
	Scores  map[string][]score `json:"scores"`
	Asked   map[string][]stamp `json:"asked"`
	Outbox  []addon.Held       `json:"outbox"`
}

// The state's limits, and the slack on a question's end.
const (
	scoreDays = 7
	askedDays = 30
	maxAsked  = 3000 // questions remembered per chat
	// settle is how long after its deadline a question is revealed, so an
	// answer sent in time that waited in the bot's queue still counts.
	settle = 3 * time.Second
	// watchSlack is how long after the quiz's end the bot keeps the watch,
	// so a late answer still stays out of the chat's history.
	watchSlack = 10 * time.Second
	// lateSlack is how late a timer may fire before the next question no
	// longer fits within the watch.
	lateSlack = 5 * time.Second
	// warnBefore is how long before a question's deadline the bot reacts
	// ⏰ to it; a question shorter than warnMin gets none, as there'd be
	// little time to warn of.
	warnBefore = 5 * time.Second
	warnMin    = 10 * time.Second
	// gapTime is the pause between a question's answer and the next
	// question, so players can read the answer.
	gapTime = 10 * time.Second
	// The lobby: how long after the last join the quiz starts, the players
	// it needs, and how long it waits for them.
	lobbyWait  = 30 * time.Second
	minPlayers = 2
	lobbyMax   = 15 * time.Minute
	// prepMax is how long a lobby's questions may take to write, as
	// players join.
	prepMax = 2 * time.Minute
)

// newServer loads the state saved in cfg.StateDir, re-arms its quizzes'
// timers and reveals at once the questions whose time passed while it was
// down.
func newServer(cfg config, clk clock, rnd *mrand.Rand, logger *log.Logger) (*server, error) {
	s := &server{
		cfg: cfg, clock: clk, log: logger, rand: rnd,
		doc:     state.Open(cfg.StateDir, "quiz"),
		slot:    make(chan struct{}, 1),
		quizzes: map[string]*quiz{},
		timers:  map[string]stopper{},
		asks:    map[string]stopper{},
		warns:   map[string]stopper{},
		gap:     cfg.gap,
		writing: map[string]bool{},
		prep:    prepMax,
		preps:   map[string]prepRun{},
		scores:  map[string][]score{},
		asked:   map[string][]stamp{},
	}
	s.srv = addon.New(addon.Config{Name: "quiz", Title: "Quiz", Tools: s.tools(), Now: clk.Now, Changed: s.save})
	var st saved
	if _, err := s.doc.Load(&st); err != nil {
		return nil, err
	}
	if st.Scores != nil {
		s.scores = st.Scores
	}
	if st.Asked != nil {
		s.asked = st.Asked
	}
	s.srv.Restore(st.Outbox)
	s.mu.Lock()
	revealed, interrupted := 0, 0
	for _, q := range st.Quizzes {
		if q == nil || s.quizzes[q.Chat] != nil || (q.Current >= len(q.Questions) && !q.Preparing) {
			continue
		}
		if q.Answers == nil {
			q.Answers = map[string]answer{}
		}
		if q.Points == nil {
			q.Points = map[string]int{}
		}
		if q.Right == nil {
			q.Right = map[string]int{}
		}
		s.quizzes[q.Chat] = q
		if q.Preparing && len(q.Questions) > 0 { // more were being written: it plays those it has
			s.toppedUpLocked(q)
			waiting := q.Waiting
			s.writtenLocked(q) // one that was ready starts now
			if !waiting {
				s.armLobby(q)
			}
			continue
		}
		if q.Preparing { // its questions were being written: they are lost
			s.callOffLocked(q, restarted)
			interrupted++
			continue
		}
		if q.Lobby {
			s.armLobby(q)
			continue
		}
		if q.Post == "" { // its question wasn't asked yet: ask it, with its full time
			if late := s.clock.Now().Sub(q.Opened); late > 0 {
				q.Opened, q.Deadline = q.Opened.Add(late), q.Deadline.Add(late)
			}
			s.scheduleAsk(q)
			s.arm(q)
			continue
		}
		if !s.clock.Now().Before(q.Deadline.Add(settle)) {
			s.revealLocked(q)
			revealed++
		} else {
			s.arm(q)
		}
	}
	open := len(s.quizzes)
	s.mu.Unlock()
	s.log.Printf("started: %d quizzes open, %d questions revealed late, %d called off while being written, %d ops held", open, revealed, interrupted, len(s.srv.Outbox()))
	if revealed+interrupted > 0 {
		s.save()
	}
	return s, nil
}

// serve runs the session with the bot on in and out until in ends or
// ctx does, as s.srv.Run does. Preparation runs in the session's context;
// once the session is ending, it stops, leaving its quiz as it is, and
// serve waits for it before returning.
func (s *server) serve(ctx context.Context, in io.Reader, out io.Writer) error {
	life, end := context.WithCancel(context.Background())
	s.mu.Lock()
	s.life, s.stopping = life, false
	s.mu.Unlock()
	halt := sync.OnceFunc(func() {
		s.mu.Lock()
		s.stopping = true
		s.mu.Unlock()
		end()
	})
	// Run's context ends only once stopping is set, so a sampling request
	// failing as the session ends is never taken for the model failing.
	rctx, rend := context.WithCancel(context.Background())
	defer rend()
	stop := context.AfterFunc(ctx, func() { halt(); rend() })
	defer stop()
	err := s.srv.Run(rctx, haltReader{in, halt}, out)
	halt()
	s.prepWG.Wait()
	return err
}

// haltReader is the session's input, calling halt before it reports its
// end or a failure.
type haltReader struct {
	r    io.Reader
	halt func()
}

func (h haltReader) Read(p []byte) (int, error) {
	n, err := h.r.Read(p)
	if err != nil {
		h.halt()
	}
	return n, err
}

// save writes quiz.json, logging a failure; the state stays in memory, and
// the next change tries again.
func (s *server) save() {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	s.mu.Lock()
	st := saved{Scores: s.scores, Asked: s.asked, Outbox: s.srv.Outbox(), Quizzes: make([]*quiz, 0, len(s.quizzes))}
	for _, q := range s.quizzes {
		st.Quizzes = append(st.Quizzes, q)
	}
	slices.SortFunc(st.Quizzes, func(a, b *quiz) int { return strings.Compare(a.Chat, b.Chat) })
	data, err := json.Marshal(st)
	s.mu.Unlock()
	if err == nil {
		err = s.doc.Save(json.RawMessage(data))
	}
	if err != nil {
		s.log.Printf("saving failed: %v", err)
	}
}

// randomID is n random bytes in hex.
func randomID(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// validID reports whether id is one of signalbot's pseudonyms with prefix
// (c_ or u_), so it can go into a {u_…} placeholder safely.
func validID(id, prefix string) bool {
	rest, ok := strings.CutPrefix(id, prefix)
	if !ok || rest == "" || len(rest) > 100 {
		return false
	}
	for _, c := range rest {
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// queue adds ops to the outbox and returns the IDs they got. The quiz's
// ops are small and have no images, so the outbox never refuses them; if
// it did, the log says so.
func (s *server) queue(ops ...addon.Op) []string {
	ids, err := s.srv.QueueIDs(ops...)
	if err != nil {
		s.log.Printf("an op was refused: %v", err)
		return make([]string, len(ops))
	}
	return ids
}
