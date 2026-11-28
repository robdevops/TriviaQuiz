package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/robdevops/signalbot/addon"
)

// The texts players see.
const (
	noChat     = "The quiz works only in a chat."
	noQuiz     = "There's no quiz on. Start one with /quiz."
	needsModel = "The quiz needs the bot's model: give the quiz addon the model permission."
	writing    = "A quiz is being written for this chat; give it a moment."
	slotBusy   = "Another chat's quiz is being written. Try again in a minute."
	tooSlow    = "The bot's model didn't write the quiz in time. Try again, perhaps with fewer questions."
	refused    = "The bot's model turned the quiz down just now; it may be over its budget for today. Try again later."
	poor       = "I couldn't write a good quiz on that topic. Try another, or try again."
	failed     = "Something went wrong writing the quiz. Try again."
	badCount   = "A quiz can have 1 to 10 questions, such as /quiz 80s music 5."
	badTime    = "Each question can have 10s to 2m, such as /quiz 80s music 5 45s."
	longTopic  = "A topic can be at most 60 characters."
	longQuiz   = "That quiz would run over 30 minutes: ask for fewer questions or less time."
	notAnswer  = "Not an answer."
	// stillWriting is posted when a lobby is ready before its questions.
	stillWriting = "🧠 Writing the questions…"
	// restarted calls off a quiz whose questions were being written when
	// the quiz stopped.
	restarted = "The quiz was interrupted while its questions were being written, so it's off. Start another with /quiz."
	// The lobby's controls, its buttons and /quiz start, wait and end,
	// which only the starter may use.
	onlyStarter = "Only the player who started this quiz can do that."
	joinedN     = "You're in: %d joined."
	alreadyIn   = "You're already in."
	starting    = "Starting!"
	needPlayers = "Waiting for at least 2 players: react to join."
	needReady   = "Waiting for at least 2 players: press ✅ to join."
	whenWritten = "Starting as soon as the questions are written."
	later       = "Starts 30s later."
	noLater     = "Can't wait any longer."
	cancelled   = "Cancelled."
	lobbyOff    = "The quiz is off."
	alreadyOn   = "That quiz has already started."
	quizOver    = "That quiz is over."
	// A lobby with buttons has no countdown, and its starter's toggle.
	noCountdown = "There's no countdown: whoever started the quiz presses ▶️ when everyone's in."
	atMost      = "Already the most."
	notEnough   = "Not enough players joined, so the quiz is off."
	notStarted  = "Nobody pressed ▶️, so the quiz is off."
)

// usage is the help for /quiz.
const usage = "Try /quiz, /quiz 80s music, /quiz space 5 45s, /quiz start, /quiz wait, /quiz end or /quiz scores."

// Points for a right answer, by difficulty, and the first right answer's
// bonus.
var points = map[string]int{"easy": 5, "medium": 10, "hard": 15}

const firstBonus = 5

// The bot's limits: what it leaves a direct command (when the call carries
// no deadline), what the quiz leaves of it to start and reply, and the
// longest post it sends.
const (
	defaultWait = 85 * time.Second
	replyTime   = 3 * time.Second
	maxPost     = 4000 // runes, once the bot puts names in
	maxNamed    = 20   // players named as right under one question
	maxRanked   = 10   // players in the results and scores
)

// tools are the quiz's tools, all for the bot alone: none is offered to
// the model.
func (s *server) tools() []addon.Tool {
	return []addon.Tool{
		{
			Name:        "quiz",
			Description: "The /quiz command: start a quiz in this chat, end it, or show its scores.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"args":{"type":"string"}}}`),
			Visibility:  []string{"bot"},
			Call: func(ctx context.Context, c *addon.Context, raw json.RawMessage) (addon.Result, error) {
				var a struct {
					Args string `json:"args"`
				}
				if err := addon.DecodeArgs(raw, &a); err != nil {
					return addon.Result{}, err
				}
				return s.command(ctx, c, a.Args), nil
			},
		},
		{
			Name:        "quiz_answer",
			Description: "Records one reply to the quiz's messages as an answer to the open question.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
			Visibility:  []string{"bot"},
			Call: func(_ context.Context, c *addon.Context, raw json.RawMessage) (addon.Result, error) {
				var a struct {
					Text     string `json:"text"`
					Reaction string `json:"reaction"` // a reaction to the quiz's messages: a join, in the lobby
					Removed  bool   `json:"removed"`
				}
				if err := addon.DecodeArgs(raw, &a); err != nil {
					return addon.Result{}, err
				}
				if a.Reaction != "" {
					return s.reacted(c, a.Removed), nil
				}
				return s.answer(c, a.Text), nil
			},
		},
		{
			Name:        "quiz_press",
			Description: "A press of one of the buttons under a quiz's lobby message: ready, a setting, start or cancel.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"press":{"type":"string"}},"required":["press"]}`),
			Visibility:  []string{"bot"},
			Call: func(_ context.Context, c *addon.Context, raw json.RawMessage) (addon.Result, error) {
				var p addon.Press
				if err := addon.DecodeArgs(raw, &p); err != nil {
					return addon.Result{}, err
				}
				return addon.Text(s.press(c, p.Press)), nil
			},
		},
	}
}

// fromChat reports whether c names a chat and a sender.
func fromChat(c *addon.Context) bool {
	return c != nil && validID(c.Chat, "c_") && validID(c.Sender, "u_")
}

// command is /quiz with its arguments.
func (s *server) command(ctx context.Context, c *addon.Context, args string) addon.Result {
	if !fromChat(c) {
		return addon.Result{Content: addon.Text(noChat).Content, IsError: true}
	}
	fields := strings.Fields(args)
	if len(fields) == 1 {
		switch strings.ToLower(fields[0]) {
		case "end", "stop":
			return s.finish(c)
		case "start":
			return addon.Text(s.control(c, "start", ""))
		case "wait":
			return addon.Text(s.control(c, "wait", ""))
		case "scores":
			return addon.Text(s.scoreboard(c.Chat))
		case "help":
			return addon.Text(usage)
		}
	}
	n, length := s.cfg.Questions, s.cfg.Time
	gotN, gotTime := false, false
	for len(fields) > 0 {
		last := fields[len(fields)-1]
		alone := len(fields) == 1 // the topic would be empty without it
		if !gotN && allDigits(last) {
			v, err := questionCount(last)
			if err == nil {
				n, gotN = v, true
				fields = fields[:len(fields)-1]
				continue
			}
			if alone {
				return addon.Text(badCount)
			}
			break
		}
		if !gotTime && startsWithDigit(last) {
			d, err := questionTime(strings.ToLower(last))
			if err == nil {
				length, gotTime = d, true
				fields = fields[:len(fields)-1]
				continue
			}
			if _, perr := time.ParseDuration(strings.ToLower(last)); perr == nil && alone {
				return addon.Text(badTime)
			}
		}
		break
	}
	topic := clean(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.Join(fields, " ")))
	if utf8.RuneCountInString(topic) > maxTopic {
		return addon.Text(longTopic)
	}
	if time.Duration(n)*(length+settle) > maxQuiz {
		return addon.Text(longQuiz)
	}
	return s.start(ctx, c, topic, n, length)
}

func allDigits(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' }) < 0
}

func startsWithDigit(s string) bool { return s != "" && s[0] >= '0' && s[0] <= '9' }

// busy is the answer to /quiz while a quiz is on, or waits in its lobby.
func busy(q *quiz) addon.Result {
	if q.Lobby && q.Buttons {
		return addon.Text("A quiz is waiting for players: press ✅ under its message to join.")
	}
	if q.Lobby {
		return addon.Text("A quiz is waiting for players: react to its message to join, or /quiz start.")
	}
	return addon.Text(fmt.Sprintf("A quiz is on: question %d of %d.", q.Current+1, size(q)))
}

// size is q's number of questions: those asked for while they are still
// being written, or in its lobby.
func size(q *quiz) int {
	if q.Preparing || q.Lobby && q.Want > 0 {
		return q.Want
	}
	return len(q.Questions)
}

// start begins a quiz in c's chat. In a group whose bot passes reactions,
// it answers at once with the lobby's intro and watch, and has the model
// write the questions meanwhile (prepare); elsewhere it has the model
// write them first, then answers with the intro, queueing question 1.
func (s *server) start(ctx context.Context, c *addon.Context, topic string, n int, length time.Duration) addon.Result {
	s.mu.Lock()
	if q := s.quizzes[c.Chat]; q != nil {
		s.mu.Unlock()
		return busy(q)
	}
	if !s.srv.CanSample() || !s.srv.WatchReplies() {
		s.mu.Unlock()
		return addon.Text(needsModel)
	}
	if s.writing[c.Chat] {
		s.mu.Unlock()
		return addon.Text(writing)
	}
	if c.Group && s.srv.WatchReactions() { // players join by reacting to the intro first
		r := s.openLobbyLocked(c, topic, n, length)
		s.mu.Unlock()
		s.save()
		return r
	}
	s.writing[c.Chat] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.writing, c.Chat)
		s.mu.Unlock()
	}()

	// Leave the bot time to send the first question.
	deadline := time.Now().Add(defaultWait)
	if c.Deadline != 0 {
		deadline = time.Unix(c.Deadline, 0).Add(-replyTime)
	}
	gctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	qs, st, took, err := s.compose(gctx, c.Chat, topic, n)
	if err != nil {
		return addon.Text(failText(err))
	}
	if ctx.Err() != nil { // the bot gave up: it won't show the question
		s.log.Printf("quiz dropped: the bot stopped waiting after %s", took)
		return addon.Text(tooSlow)
	}
	s.mu.Lock()
	if q := s.quizzes[c.Chat]; q != nil {
		s.mu.Unlock()
		return busy(q)
	}
	now := s.clock.Now()
	q := &quiz{
		ID: "q" + randomID(6), Chat: c.Chat, Network: s.network(c), Starter: c.Sender, Topic: topic,
		Questions: qs, Time: length,
		Started: now, End: now.Add(time.Duration(len(qs))*(length+settle) + time.Duration(len(qs)-1)*s.gap),
		Opened: now, Deadline: now.Add(length),
		Answers: map[string]answer{}, Points: map[string]int{}, Right: map[string]int{},
	}
	s.quizzes[c.Chat] = q
	s.remember(c.Chat, qs, now)
	if !c.Group { // a DM: its one player, so no question waits for the timer
		q.Players = []string{c.Sender}
	}
	s.arm(q)
	// The question is a post of its own, so its answer can reply to it. It
	// waits in the chat's mailbox behind this call's answer, the intro, so
	// it always comes second, once the watch that takes its replies is on.
	q.Post = s.queue(addon.Op{Op: "post", Chat: q.Chat, Text: clamp(questionText(q), maxPost)})[0]
	r := s.opening(q)
	s.mu.Unlock()
	s.save()
	s.log.Printf("quiz %s started: %d questions of %s, %d sampling rounds in %s, %d rejected, %d repeats",
		q.ID, len(qs), length, st.rounds, took, st.rejected, st.repeats)
	return r
}

// openLobbyLocked opens a lobby for a quiz of n questions in c's chat,
// starts preparing its questions, and returns the intro with the watch.
// The watch covers the lobby, preparation as far as it can outlast the
// lobby, and the quiz. s.mu is held.
func (s *server) openLobbyLocked(c *addon.Context, topic string, n int, length time.Duration) addon.Result {
	now := s.clock.Now()
	q := &quiz{
		ID: "q" + randomID(6), Chat: c.Chat, Network: s.network(c), Starter: c.Sender, Topic: topic,
		Want: n, Time: length, Started: now,
		End:     now.Add(max(lobbyMax, s.prep) + time.Duration(n)*(length+settle) + time.Duration(n-1)*s.gap),
		Answers: map[string]answer{}, Points: map[string]int{}, Right: map[string]int{},
		Lobby: true, CallOff: now.Add(lobbyMax),
	}
	if c.Buttons {
		// The watch covers the toggle's larger setting, and a top-up of
		// the questions begun as late as the lobby's end.
		q.Buttons, q.BaseWant = true, n
		mn := max(n, doubled(n, maxQuestions))
		q.End = now.Add(lobbyMax + s.prep + time.Duration(mn)*(length+settle) + time.Duration(mn-1)*s.gap)
	}
	s.quizzes[c.Chat] = q
	s.armLobby(q)
	s.prepareLocked(q, n)
	s.log.Printf("quiz %s opened its lobby: writing %d questions of %s", q.ID, n, length)
	return s.opening(q)
}

// prepareLocked has the model write n more of q's questions, within
// s.prep, marking q as preparing. s.mu is held.
func (s *server) prepareLocked(q *quiz, n int) {
	base := s.life
	if base == nil {
		base = context.Background()
	}
	pctx, cancel := context.WithTimeout(base, s.prep)
	q.Preparing = true
	s.prepGen++
	s.preps[q.ID] = prepRun{cancel, s.prepGen}
	s.prepWG.Add(1)
	go s.prepare(pctx, s.prepGen, q.ID, q.Chat, q.Topic, n)
}

// doubled is the toggle's second mode: twice v, up to most.
func doubled[T int | time.Duration](v, most T) T {
	return min(2*v, most)
}

// opening is the answer that starts q: its intro, and the watch for the
// whole quiz; in a lobby, on a bot that shows buttons, its one row of
// buttons too. s.mu is held.
func (s *server) opening(q *quiz) addon.Result {
	r := addon.Result{
		Content: addon.Text(introText(q)).Content,
		Meta: &addon.ResultMeta{Watch: &addon.Watch{
			ID: q.ID, Replies: true, Reactions: q.Lobby, Until: q.End.Add(watchSlack).Format(time.RFC3339), Tool: "quiz_answer", Over: "That quiz is over. Start another with /quiz.",
		}},
	}
	if q.Lobby && q.Buttons {
		r.Meta.Buttons = lobbyRows(q)
		r.Meta.OnPress = "quiz_press"
	}
	return r
}

// lobbyRows are the buttons under q's lobby intro, all in one row: ✅,
// Ready, with how many joined, for anyone; the starter's toggle of the
// number of rounds (questions), showing it; ▶️, Start, once minPlayers
// have joined; and ✖️, Cancel. The answer time is the command's or the
// setting's.
func lobbyRows(q *quiz) [][]addon.Button {
	row := []addon.Button{
		{Label: fmt.Sprintf("✅ %d", len(q.Joined)), Data: "ready:" + q.ID},
		{Label: fmt.Sprintf("Rounds: %d", q.Want), Data: "t1:" + q.ID},
	}
	if len(q.Joined) >= minPlayers {
		row = append(row, addon.Button{Label: "▶️", Data: "start:" + q.ID})
	}
	row = append(row, addon.Button{Label: "✖️", Data: "cancel:" + q.ID})
	return [][]addon.Button{row}
}

// showLocked replaces the buttons under q's lobby intro with its rows as
// they are now, after a join, a leave or a toggle. s.mu is held.
func (s *server) showLocked(q *quiz) {
	if q.Lobby && q.Buttons {
		s.queue(addon.Op{Op: "buttons", Chat: q.Chat, Watch: q.ID, Buttons: lobbyRows(q)})
	}
}

// retextLocked is showLocked rewriting q's lobby intro too, as it would be
// written now, after a toggle of its number of rounds. s.mu is held.
func (s *server) retextLocked(q *quiz) {
	if q.Lobby && q.Buttons {
		s.queue(addon.Op{Op: "buttons", Chat: q.Chat, Watch: q.ID, Buttons: lobbyRows(q), Text: introText(q)})
	}
}

// introText is q's intro as its opening writes it. A lobby's on a button
// network shows the number of rounds too, which retextLocked keeps up to
// date.
func introText(q *quiz) string {
	return clamp(intro(q, q.Lobby && q.Buttons), maxPost)
}

// compose has the model write n questions on topic for chat within ctx,
// once the slot is free, and frees the slot. A request given up on frees
// the bot's place at once, so the next quiz needn't wait for it.
func (s *server) compose(ctx context.Context, chat, topic string, n int) ([]question, genStats, time.Duration, error) {
	select {
	case s.slot <- struct{}{}:
	default:
		select {
		case s.slot <- struct{}{}:
		case <-ctx.Done():
			return nil, genStats{}, 0, errBusy
		}
	}
	began := time.Now()
	qs, st, err := s.write(ctx, chat, topic, n)
	<-s.slot
	took := time.Since(began).Round(100 * time.Millisecond)
	if err != nil {
		s.log.Printf("no quiz: %v, after %d sampling rounds in %s, %d rejected, %d repeats", err, st.rounds, took, st.rejected, st.repeats)
	}
	return qs, st, took, err
}

// failText is what players are told when the quiz couldn't be written.
func failText(err error) string {
	switch {
	case errors.Is(err, errTooSlow):
		return tooSlow
	case errors.Is(err, errBusy):
		return slotBusy
	case errors.Is(err, errRefused):
		return refused
	case errors.Is(err, errPoor):
		return poor
	}
	return failed
}

// prepare writes n questions of the quiz id in chat, in its lobby,
// within ctx: prepMax, or until the lobby is called off or the session
// ends, adding them to those written before. Once written, the quiz starts
// if its lobby is ready and still has minPlayers (else the lobby waits for
// joins again); if they can't be, the quiz is called off with why. If the
// starter asked for more meanwhile, more are written first; if a top-up
// fails, the quiz plays those it has. A quiz called off or started
// meanwhile, or a session ending, leaves it all as it is.
func (s *server) prepare(ctx context.Context, gen int, id, chat, topic string, n int) {
	defer s.prepWG.Done()
	qs, st, took, err := s.compose(ctx, chat, topic, n)
	s.mu.Lock()
	run, mine := s.preps[id]
	mine = mine && run.gen == gen
	if mine {
		run.stop()
		delete(s.preps, id)
	}
	q := s.quizzes[chat]
	switch {
	case s.stopping:
		s.mu.Unlock()
		s.log.Printf("quiz %s: writing stopped, as the session ended", id)
		return
	case q == nil || q.ID != id || !q.Preparing || !mine:
		s.mu.Unlock()
		s.log.Printf("quiz %s: writing dropped, as the lobby was called off or no longer needs it", id)
		return
	case err != nil && len(q.Questions) == 0:
		s.callOffLocked(q, failText(err))
	default:
		if err != nil {
			s.log.Printf("quiz %s: writing %d more failed, so it plays the %d it has", id, n, len(q.Questions))
			s.toppedUpLocked(q)
		} else {
			q.Questions = append(q.Questions, qs...)
			s.remember(chat, qs, s.clock.Now())
			s.log.Printf("quiz %s written: %d questions, %d sampling rounds in %s, %d rejected, %d repeats",
				id, len(qs), st.rounds, took, st.rejected, st.repeats)
			if more := q.Want - len(q.Questions); more > 0 && len(qs) == n { // the starter asked for more meanwhile
				s.prepareLocked(q, more)
				break
			}
		}
		s.writtenLocked(q)
	}
	s.mu.Unlock()
	s.save()
}

// intro is the quiz's first lines: its topic, its length, and how to
// answer; a lobby's, how to join: by ✅ (Ready) where buttons show, else
// by reacting.
func intro(q *quiz, buttons bool) string {
	title := "🧠 **Quiz**"
	if q.Topic != "" {
		title = "🧠 **Quiz: " + q.Topic + "**"
	}
	how := "Answer by **replying** to the quiz's messages: the letter for a multiple-choice question, or the answer itself. Your last answer before time is up counts: edit it, or reply again, to change it. "
	if q.Lobby && buttons { // the starter may change its number of rounds, and the intro is rewritten
		return fmt.Sprintf("%s · %d %s, %s each.\n👋 **Press ✅ to join**, whoever started it too. The starter can change the number of rounds with its button, and presses ▶️ when everyone's in (it shows once at least 2 have joined; ✖️ calls it off).\n%sA question ends as soon as everyone who joined has answered.",
			title, size(q), plural(size(q), "question", "questions"), lengthLabel(q.Time), how)
	}
	if q.Lobby {
		return fmt.Sprintf("%s · %d %s, %s each.\n👋 **React to this message with any emoji to join**, whoever started it too: the quiz starts 30 seconds after the last player joins, once at least 2 have.\n%sA question ends as soon as everyone who joined has answered.",
			title, size(q), plural(size(q), "question", "questions"), lengthLabel(q.Time), how)
	}
	return fmt.Sprintf("%s · %d %s, %s each.\n%sFrom question 2 on, a question ends as soon as everyone who answered question 1 has answered.",
		title, len(q.Questions), plural(len(q.Questions), "question", "questions"), lengthLabel(q.Time), how)
}

// questionText is the open question, with its choices, and how to answer
// it.
func questionText(q *quiz) string {
	qn := q.Questions[q.Current]
	var b strings.Builder
	fmt.Fprintf(&b, "**Question %d of %d** · %s · %s\n%s", q.Current+1, len(q.Questions), qn.Difficulty, lengthLabel(q.Deadline.Sub(q.Opened)), qn.Q)
	if qn.Kind == "choice" {
		for i, c := range qn.Choices {
			b.WriteString("\n" + string(rune('A'+i)) + ". " + c)
		}
		b.WriteString("\n↩️ Reply to this message with A, B, C or D.")
	} else {
		b.WriteString("\n↩️ Reply to this message with your answer.")
	}
	return b.String()
}

// lengthLabel is a question's time as players see it: 30s, 2 min, 1m30s.
func lengthLabel(d time.Duration) string {
	d = d.Round(time.Second)
	switch m, sec := int(d/time.Minute), int(d%time.Minute/time.Second); {
	case m == 0:
		return strconv.Itoa(sec) + "s"
	case sec == 0:
		return strconv.Itoa(m) + " min"
	default:
		return fmt.Sprintf("%dm%ds", m, sec)
	}
}

// arm sets q's timer to reveal its open question once its time is up,
// and its warning, warnBefore its deadline: unless the question is
// shorter than warnMin, or has less than warnBefore left, such as once it
// ended early. s.mu is held.
func (s *server) arm(q *quiz) {
	chat, id, current, deadline := q.Chat, q.ID, q.Current, q.Deadline
	if t := s.timers[chat]; t != nil {
		t.Stop()
	}
	now := s.clock.Now()
	s.timers[chat] = s.clock.AfterFunc(q.Deadline.Add(settle).Sub(now), func() { s.timeUp(chat, id, current) })
	if t := s.warns[chat]; t != nil {
		t.Stop()
		delete(s.warns, chat)
	}
	if at := q.Deadline.Add(-warnBefore); q.Deadline.Sub(q.Opened) >= warnMin && at.After(now) {
		s.warns[chat] = s.clock.AfterFunc(at.Sub(now), func() { s.warn(chat, id, current, deadline) })
	}
}

// warn reacts ⏰ (😱 on Telegram, see warnEmoji) to question current of the quiz id in chat, if it is
// still open with the deadline it was armed for, so not ended early. A
// question with no post ID, its post refused by the outbox, gets none.
// The reaction counts against the chat's rate like a post: one more per
// question fits the bot's default.
func (s *server) warn(chat, id string, current int, deadline time.Time) {
	s.mu.Lock()
	q := s.quizzes[chat]
	if q == nil || q.ID != id || q.Lobby || q.Current != current || !q.Deadline.Equal(deadline) || q.Post == "" {
		s.mu.Unlock()
		return
	}
	delete(s.warns, chat)
	s.queue(addon.Op{Op: "react", Chat: q.Chat, Post: q.Post, Emoji: warnEmoji(q.Network)})
	s.mu.Unlock()
	s.save()
}

// warnEmoji is the reaction for 5 seconds left on network: Telegram
// takes only some emoji as reactions, ⏰ not among them, so there it is
// 😱; elsewhere ⏰.
func warnEmoji(network string) string {
	if network == "telegram" {
		return "😱"
	}
	return "⏰"
}

// network is the network of c's chat: its Context's, on a bot on several,
// else the bot's one.
func (s *server) network(c *addon.Context) string {
	if c.Network != "" {
		return c.Network
	}
	return s.srv.Network()
}

// timeUp reveals question current of the quiz id in chat, if it is still
// open.
func (s *server) timeUp(chat, id string, current int) {
	s.mu.Lock()
	q := s.quizzes[chat]
	if q == nil || q.ID != id || q.Current != current {
		s.mu.Unlock()
		return
	}
	s.revealLocked(q)
	s.mu.Unlock()
	s.save()
}

// revealLocked marks q's open question and queues one post with its
// answer, who got it right, and the next question; after the last, or
// when too little of the watch is left for the next, the results instead,
// and the end of the watch. A question opened late, after a restart, gets
// only the time the watch has left. s.mu is held.
func (s *server) revealLocked(q *quiz) {
	now := s.clock.Now()
	if q.Current == 0 && len(q.Players) == 0 { // no lobby: question 1's players are the quiz's
		q.Players = slices.Sorted(maps.Keys(q.Answers))
	}
	revealed := s.mark(q)
	next := q.Current + 1
	length := min(q.Time, q.End.Add(lateSlack).Sub(now)-s.gap-settle)
	if next < len(q.Questions) && length >= minTime {
		s.queue(s.reply(q, revealed))
		// The next question opens after the gap, with its full time.
		opens := now.Add(s.gap)
		q.Current, q.Opened, q.Deadline, q.Answers, q.Post = next, opens, opens.Add(length), map[string]answer{}, ""
		s.arm(q)
		s.scheduleAsk(q)
		return
	}
	if next < len(q.Questions) {
		s.log.Printf("quiz %s cut short: %d questions left that no longer fit", q.ID, len(q.Questions)-next)
	}
	results := s.end(q, now)
	s.queue(s.reply(q, revealed), addon.Op{Op: "post", Chat: q.Chat, Text: clamp(results, maxPost)}, addon.Op{Op: "unwatch", Chat: q.Chat, Watch: q.ID})
}

// scheduleAsk posts q's open question when it opens: at once if it has,
// else by a timer. s.mu is held.
func (s *server) scheduleAsk(q *quiz) {
	if t := s.asks[q.Chat]; t != nil {
		t.Stop()
		delete(s.asks, q.Chat)
	}
	wait := q.Opened.Sub(s.clock.Now())
	if wait <= 0 {
		s.askLocked(q)
		return
	}
	chat, id, current := q.Chat, q.ID, q.Current
	s.asks[chat] = s.clock.AfterFunc(wait, func() {
		s.mu.Lock()
		q := s.quizzes[chat]
		if q == nil || q.ID != id || q.Current != current || q.Post != "" {
			s.mu.Unlock()
			return
		}
		delete(s.asks, chat)
		s.askLocked(q)
		s.mu.Unlock()
		s.save()
	})
}

// askLocked posts q's open question, which its answer will reply to. s.mu
// is held.
func (s *server) askLocked(q *quiz) {
	q.Post = s.queue(addon.Op{Op: "post", Chat: q.Chat, Text: clamp(questionText(q), maxPost)})[0]
}

// reply is the post of a question's answer, as a reply to the question.
func (s *server) reply(q *quiz, revealed string) addon.Op {
	return addon.Op{Op: "post", Chat: q.Chat, Text: clamp(revealed, maxPost), ReplyTo: q.Post}
}

// mark scores q's open question and returns its reveal: the answer, the
// explanation, and who got it right, as {u_…} placeholders, the first
// marked ⚡. s.mu is held.
func (s *server) mark(q *quiz) string {
	qn := q.Questions[q.Current]
	type right struct {
		user string
		answer
	}
	var rights []right
	for user, a := range q.Answers {
		if a.Right {
			rights = append(rights, right{user, a})
		}
	}
	slices.SortFunc(rights, func(a, b right) int { return cmp.Or(a.At.Compare(b.At), cmp.Compare(a.Seq, b.Seq)) })
	for i, r := range rights {
		p := points[qn.Difficulty]
		if i == 0 {
			p += firstBonus
		}
		q.Points[r.user] += p
		q.Right[r.user]++
	}
	var b strings.Builder
	b.WriteString("✅ **" + answerLabel(qn) + "**: " + qn.Explanation)
	switch {
	case len(rights) == 0:
		b.WriteString("\nNobody got it.")
	default:
		b.WriteString("\nRight: ")
		for i, r := range rights[:min(len(rights), maxNamed)] {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString("{" + r.user + "}")
			if i == 0 {
				b.WriteString(" ⚡")
			}
		}
		if more := len(rights) - maxNamed; more > 0 {
			fmt.Fprintf(&b, " and %d more", more)
		}
	}
	return b.String()
}

// answerLabel is a question's answer as the reveal gives it: "B. Paris",
// or a text question's first accepted answer.
func answerLabel(qn question) string {
	if qn.Kind == "choice" {
		return string(rune('A'+qn.Answer)) + ". " + qn.Choices[qn.Answer]
	}
	return qn.Answers[0]
}

// finish is /quiz end, which only the player who started the quiz may use.
// It answers with the open question's answer and the results, and queues
// the end of the quiz's watch.
func (s *server) finish(c *addon.Context) addon.Result {
	s.mu.Lock()
	q := s.quizzes[c.Chat]
	switch {
	case q == nil:
		s.mu.Unlock()
		return addon.Text(noQuiz)
	case q.Starter != c.Sender:
		s.mu.Unlock()
		return addon.Text("Only the player who started this quiz can end it.")
	case q.Lobby:
		s.callOffLocked(q, "")
		s.mu.Unlock()
		s.save()
		return addon.Text(lobbyOff)
	}
	revealed := ""
	if q.Post != "" { // the open question was asked: reveal it first
		revealed = s.mark(q) + "\n\n"
	}
	text := revealed + s.end(q, s.clock.Now())
	s.queue(addon.Op{Op: "unwatch", Chat: q.Chat, Watch: q.ID})
	s.mu.Unlock()
	s.save()
	return addon.Text(clamp(text, maxPost))
}

// end closes q, adding its points to the chat's scores, and returns its
// results. s.mu is held.
func (s *server) end(q *quiz, now time.Time) string {
	delete(s.quizzes, q.Chat)
	for _, m := range []map[string]stopper{s.timers, s.asks, s.warns} {
		if t := m[q.Chat]; t != nil {
			t.Stop()
			delete(m, q.Chat)
		}
	}
	type placing struct {
		user          string
		points, right int
	}
	var placings []placing
	for user, p := range q.Points {
		placings = append(placings, placing{user, p, q.Right[user]})
	}
	slices.SortFunc(placings, func(a, b placing) int {
		return cmp.Or(cmp.Compare(b.points, a.points), strings.Compare(a.user, b.user))
	})
	s.pruneScores(now)
	for _, p := range placings {
		s.scores[q.Chat] = append(s.scores[q.Chat], score{User: p.user, Points: p.points, At: now})
	}
	s.log.Printf("quiz %s ended after %d of %d questions: %d players scored", q.ID, q.Current+1, len(q.Questions), len(placings))
	asked := q.Current + 1
	if len(placings) == 0 {
		return "🏁 **Quiz over.** Nobody scored."
	}
	var b strings.Builder
	b.WriteString("🏁 **Quiz over**")
	for i, p := range placings[:min(len(placings), maxRanked)] {
		fmt.Fprintf(&b, "\n%s {%s} %d · %d/%d right", medal(i+1), p.user, p.points, p.right, asked)
	}
	return b.String()
}

// medal marks a rank: a medal for the first three, else the number.
func medal(rank int) string {
	if rank <= 3 {
		return []string{"🥇", "🥈", "🥉"}[rank-1]
	}
	return strconv.Itoa(rank) + "."
}

// answer records one reply to the quiz's messages as its sender's answer
// to the open question: only whether it is right, never what it said. A
// reply to a choice question that isn't a letter A to D is no answer, and
// leaves the player free to answer. A player's last answer before the
// deadline counts, by when the bot got it: a new reply or an edit replaces
// an earlier answer.
// A recorded answer gets no reaction: reactions take from the chat's post
// budget, and a reveal post refused by it is lost.
func (s *server) answer(c *addon.Context, msg string) addon.Result {
	if !fromChat(c) {
		return addon.Text(noChat)
	}
	at := s.clock.Now()
	if t := cmp.Or(c.Edited, c.Sent); t > 0 {
		at = time.Unix(t, 0)
	}
	s.mu.Lock()
	q := s.quizzes[c.Chat]
	switch {
	case q == nil:
		s.mu.Unlock()
		return addon.Text(noQuiz)
	case q.Lobby:
		s.mu.Unlock()
		return addon.Text("The quiz hasn't started yet.")
	case c.Sent > 0 && c.Sent < q.Started.Unix():
		s.mu.Unlock()
		return addon.Text("That message is from before this quiz.")
	case c.Sent > 0 && time.Unix(c.Sent, 0).Before(q.Opened): // an edit of an answer to an earlier question
		s.mu.Unlock()
		return addon.Text("Too late for that question.")
	case at.Before(q.Opened) || at.After(q.Deadline): // at is in whole seconds
		s.mu.Unlock()
		return addon.Text("Too late for that question.")
	}
	if prev, ok := q.Answers[c.Sender]; ok && at.Before(prev.At) { // an edit of an older answer, arriving late
		s.mu.Unlock()
		return addon.Text("A later answer of yours counts.")
	}
	qn := q.Questions[q.Current]
	var right bool
	if qn.Kind == "choice" {
		choice, ok := letter(msg)
		if !ok {
			s.mu.Unlock()
			return addon.Text(notAnswer)
		}
		right = choice == qn.Answer
	} else {
		right = markText(msg, qn.Answers)
	}
	q.Seq++
	q.Answers[c.Sender] = answer{Right: right, At: at, Seq: q.Seq}
	if s.allAnswered(q) {
		// Everyone playing has answered: end the question now, revealing it
		// after settle, as at its deadline, for answers still on their way.
		q.Deadline = s.clock.Now()
		s.arm(q)
		s.log.Printf("quiz %s question %d ended early", q.ID, q.Current+1)
	}
	s.mu.Unlock()
	s.save()
	return addon.Text("Recorded.")
}

// reacted is a reaction to the quiz's messages: in the lobby, a player
// joining, or leaving if removed; after it, nothing.
func (s *server) reacted(c *addon.Context, removed bool) addon.Result {
	if !fromChat(c) {
		return addon.Text(noChat)
	}
	s.mu.Lock()
	q := s.quizzes[c.Chat]
	if q == nil || !q.Lobby {
		s.mu.Unlock()
		return addon.Text("Noted.")
	}
	in := slices.Contains(q.Joined, c.Sender)
	switch {
	case !removed && !in:
		s.joinLocked(q, c.Sender)
	case removed && in:
		q.Joined = slices.DeleteFunc(q.Joined, func(u string) bool { return u == c.Sender })
		s.armLobby(q)
		s.showLocked(q)
	default:
		s.mu.Unlock()
		return addon.Text("Noted.")
	}
	s.mu.Unlock()
	s.save()
	return addon.Text("Noted.")
}

// joinLocked adds u to q's lobby, if not in it yet, as its last join,
// re-arming the lobby's timer, and reports whether u was added: a join by
// reaction or by ✅ (Ready) alike. The caller saves. s.mu is held.
func (s *server) joinLocked(q *quiz, u string) bool {
	if slices.Contains(q.Joined, u) {
		return false
	}
	q.Joined = append(q.Joined, u)
	q.LastJoin = s.clock.Now()
	s.armLobby(q)
	s.showLocked(q)
	return true
}

// armLobby sets q's lobby timer: with enough players, to start at
// startAt, or, if its questions aren't written yet, to say so and wait for
// them; else to call the quiz off at CallOff. A lobby with buttons has no
// countdown: only its starter starts it, and it is called off at CallOff
// all the same. A lobby that is waiting has no timer. s.mu is held.
func (s *server) armLobby(q *quiz) {
	if q.Waiting { // ready: it starts once its questions are written
		return
	}
	at := q.CallOff
	if len(q.Joined) >= minPlayers && !q.Buttons {
		at = startAt(q)
	}
	chat, id := q.Chat, q.ID
	if t := s.timers[chat]; t != nil {
		t.Stop()
	}
	s.timers[chat] = s.clock.AfterFunc(max(at.Sub(s.clock.Now()), 0), func() {
		s.mu.Lock()
		q := s.quizzes[chat]
		if q == nil || q.ID != id || !q.Lobby || q.Waiting { // started, called off or ready meanwhile
			s.mu.Unlock()
			return
		}
		now := s.clock.Now()
		switch {
		case len(q.Joined) >= minPlayers && !q.Buttons && !now.Before(startAt(q)):
			s.lobbyDueLocked(q)
		case len(q.Joined) < minPlayers && !now.Before(q.CallOff):
			s.callOffLocked(q, notEnough)
		case q.Buttons && !now.Before(q.CallOff):
			s.callOffLocked(q, notStarted)
		default:
			s.armLobby(q)
		}
		s.mu.Unlock()
		s.save()
	})
}

// startAt is when q's lobby, with enough players, starts: lobbyWait after
// the last join, plus the starter's extra wait, but never after CallOff.
func startAt(q *quiz) time.Time {
	at := q.LastJoin.Add(lobbyWait + q.Extra)
	if at.After(q.CallOff) {
		return q.CallOff
	}
	return at
}

// lobbyDueLocked ends q's lobby, ready with enough players: question 1
// opens now, or, if its questions aren't written yet, the lobby says so
// and waits for them. It is the one way a lobby starts, whether its timer
// ran out or the starter started it. s.mu is held.
func (s *server) lobbyDueLocked(q *quiz) {
	if t := s.timers[q.Chat]; t != nil {
		t.Stop()
		delete(s.timers, q.Chat)
	}
	s.enoughLocked(q)
	if q.Preparing {
		q.Waiting = true
		s.queue(addon.Op{Op: "post", Chat: q.Chat, Text: stillWriting})
		s.log.Printf("quiz %s ready: waiting for its questions", q.ID)
		return
	}
	s.beginLocked(q)
}

// press is a press of one of the lobby's buttons, its data the action and
// the quiz's ID; it returns the pop-up.
func (s *server) press(c *addon.Context, data string) string {
	action, id, _ := strings.Cut(data, ":")
	if id == "" || action == "wait" { // ⏱ +30s, under an older lobby
		return quizOver
	}
	return s.control(c, action, id)
}

// control is ✅ (Ready), anyone's join, or one of the starter's lobby
// controls, start, cancel, wait or the toggle t1,
// from a button pressed under the quiz id, or typed, with id "". It
// returns what to tell the player: a press's pop-up, or a command's
// answer. Its work is all in memory, its posts queued, so a press answers
// at once.
func (s *server) control(c *addon.Context, action, id string) string {
	if !fromChat(c) {
		return noChat
	}
	s.mu.Lock()
	q := s.quizzes[c.Chat]
	switch {
	case q == nil && id == "":
		s.mu.Unlock()
		return noQuiz
	case q == nil || id != "" && q.ID != id:
		s.mu.Unlock()
		return quizOver
	case !q.Lobby:
		s.mu.Unlock()
		return alreadyOn
	case action == "ready": // anyone's, the starter's too
		text := alreadyIn
		if s.joinLocked(q, c.Sender) {
			text = fmt.Sprintf(joinedN, len(q.Joined))
		}
		s.mu.Unlock()
		s.save()
		return text
	case q.Starter != c.Sender:
		s.mu.Unlock()
		return onlyStarter
	}
	var text string
	switch action {
	case "start":
		text = s.startNowLocked(q)
	case "cancel": // as /quiz end does in a lobby, but saying so in the chat
		s.callOffLocked(q, lobbyOff)
		text = cancelled
	case "wait":
		text = s.waitLocked(q)
	case "t1":
		if !q.Buttons || id == "" {
			s.mu.Unlock()
			return quizOver
		}
		text = s.toggleLocked(q)
	case "t2": // an older lobby's Answer timer: it changes nothing, and the row loses it
		if !q.Buttons || id == "" {
			s.mu.Unlock()
			return quizOver
		}
		s.showLocked(q)
		text = "Answer timer: " + lengthLabel(q.Time)
	default:
		s.mu.Unlock()
		return quizOver
	}
	s.mu.Unlock()
	s.save()
	return text
}

// startNowLocked is the starter starting q's lobby: it counts as their
// join, and with minPlayers the lobby ends as when its timer runs out.
// s.mu is held.
func (s *server) startNowLocked(q *quiz) string {
	if q.Waiting {
		return whenWritten
	}
	joined := !slices.Contains(q.Joined, q.Starter)
	if joined {
		q.Joined = append(q.Joined, q.Starter)
		q.LastJoin = s.clock.Now()
	}
	if len(q.Joined) < minPlayers {
		s.armLobby(q)
		s.showLocked(q)
		if q.Buttons {
			return needReady
		}
		return needPlayers
	}
	s.lobbyDueLocked(q)
	if joined { // still a lobby, waiting for its questions: its count shows
		s.showLocked(q)
	}
	if q.Waiting {
		return whenWritten
	}
	return starting
}

// waitLocked adds lobbyWait to q's lobby's start, for this and every later
// countdown, as long as it still starts by CallOff. s.mu is held.
func (s *server) waitLocked(q *quiz) string {
	if q.Buttons {
		return noCountdown
	}
	if q.Waiting { // its countdown is over
		return noLater
	}
	base := q.LastJoin
	if len(q.Joined) < minPlayers { // no countdown yet: one starting now
		base = s.clock.Now()
	}
	room := q.CallOff.Sub(base.Add(lobbyWait + q.Extra))
	if room <= 0 {
		return noLater
	}
	q.Extra += min(lobbyWait, room)
	s.armLobby(q)
	if room < lobbyWait {
		return noLater
	}
	return later
}

// toggleLocked is the starter's press of q's toggle, t1, its number of
// questions: it switches between what the lobby opened with and double
// that, up to the limit; one already at the limit stays. More questions
// than are written are written now, in the background. It returns the
// pop-up. s.mu is held.
func (s *server) toggleLocked(q *quiz) string {
	if q.Waiting {
		return whenWritten
	}
	alt := doubled(q.BaseWant, maxQuestions)
	if alt <= q.BaseWant {
		return atMost
	}
	want := alt
	if q.Want != q.BaseWant {
		want = q.BaseWant
	}
	if time.Duration(want)*(q.Time+settle) > maxQuiz {
		return longQuiz
	}
	q.Want = want
	if !q.Preparing && len(q.Questions) < q.Want {
		s.prepareLocked(q, q.Want-len(q.Questions))
	}
	s.enoughLocked(q)
	s.retextLocked(q)
	return fmt.Sprintf("Rounds: %d", want)
}

// enoughLocked stops writing more of q's questions once it has as many as
// it wants, as after a toggle back. s.mu is held.
func (s *server) enoughLocked(q *quiz) {
	if !q.Preparing || len(q.Questions) == 0 || len(q.Questions) < q.Want {
		return
	}
	if run, ok := s.preps[q.ID]; ok {
		run.stop()
		delete(s.preps, q.ID)
	}
	q.Preparing = false
}

// toppedUpLocked has q, whose top-up ended without all it wanted (it
// failed, or a restart lost it), play the questions it has, and its lobby
// show that number: what it opened with, if those are written, else as many
// as are. s.mu is held.
func (s *server) toppedUpLocked(q *quiz) {
	if len(q.Questions) >= q.BaseWant {
		q.Want = q.BaseWant
	} else {
		q.Want = len(q.Questions)
	}
	s.retextLocked(q)
}

// writtenLocked is q's questions written: a lobby that was ready starts
// now, if its players stayed, else goes back to waiting for joins, or
// CallOff. s.mu is held.
func (s *server) writtenLocked(q *quiz) {
	q.Preparing = false
	if q.Waiting { // the lobby was ready: start now, if its players stayed
		q.Waiting = false
		if len(q.Joined) >= minPlayers {
			s.beginLocked(q)
		} else { // back to waiting for joins, or CallOff
			s.armLobby(q)
		}
	}
}

// beginLocked ends q's lobby: its players are those who joined, and
// question 1 opens now, with as many questions as it wants. s.mu is held.
func (s *server) beginLocked(q *quiz) {
	if q.Want > 0 && len(q.Questions) > q.Want {
		q.Questions = q.Questions[:q.Want]
	}
	now := s.clock.Now()
	q.Lobby, q.Players = false, slices.Clone(q.Joined)
	q.Started, q.Opened, q.Deadline = now, now, now.Add(q.Time)
	s.arm(q)
	s.askLocked(q)
	s.log.Printf("quiz %s began: %d players", q.ID, len(q.Players))
}

// callOffLocked ends q in its lobby, saying why if why isn't "", ends its
// watch, and stops writing its questions. s.mu is held.
func (s *server) callOffLocked(q *quiz, why string) {
	if run, ok := s.preps[q.ID]; ok {
		run.stop()
		delete(s.preps, q.ID)
	}
	if why != "" {
		s.queue(addon.Op{Op: "post", Chat: q.Chat, Text: why})
	}
	s.queue(addon.Op{Op: "unwatch", Chat: q.Chat, Watch: q.ID})
	delete(s.quizzes, q.Chat)
	for _, m := range []map[string]stopper{s.timers, s.asks, s.warns} {
		if t := m[q.Chat]; t != nil {
			t.Stop()
			delete(m, q.Chat)
		}
	}
	s.log.Printf("quiz %s called off in its lobby: %d joined", q.ID, len(q.Joined))
}

// allAnswered reports whether a question after the first has an answer
// from each player of question 1, and is still before its deadline. s.mu
// is held.
func (s *server) allAnswered(q *quiz) bool {
	if len(q.Players) == 0 || !s.clock.Now().Before(q.Deadline) { // none yet, before the end of question 1, without a lobby
		return false
	}
	for _, p := range q.Players {
		if _, ok := q.Answers[p]; !ok {
			return false
		}
	}
	return true
}

// scoreboard is /quiz scores: the chat's totals over the last scoreDays.
func (s *server) scoreboard(chat string) string {
	s.mu.Lock()
	s.pruneScores(s.clock.Now())
	type total struct {
		user            string
		points, quizzes int
	}
	byUser := map[string]*total{}
	var totals []*total
	for _, sc := range s.scores[chat] {
		t := byUser[sc.User]
		if t == nil {
			t = &total{user: sc.User}
			byUser[sc.User] = t
			totals = append(totals, t)
		}
		t.points += sc.Points
		t.quizzes++
	}
	s.mu.Unlock()
	if len(totals) == 0 {
		return "No quiz scores in the last 7 days. Start a quiz with /quiz."
	}
	// Scores are kept in the order they were won, so a tie goes to who
	// scored first.
	slices.SortStableFunc(totals, func(a, b *total) int { return cmp.Compare(b.points, a.points) })
	var b strings.Builder
	b.WriteString("🏆 **Quiz scores, last 7 days**")
	for i, t := range totals[:min(len(totals), maxRanked)] {
		fmt.Fprintf(&b, "\n%s {%s} %d · %d %s", medal(i+1), t.user, t.points, t.quizzes, plural(t.quizzes, "quiz", "quizzes"))
	}
	return b.String()
}

// pruneScores forgets scores older than scoreDays. s.mu is held.
func (s *server) pruneScores(now time.Time) {
	since := now.Add(-scoreDays * 24 * time.Hour)
	for chat, list := range s.scores {
		s.scores[chat] = slices.DeleteFunc(list, func(sc score) bool { return sc.At.Before(since) })
		if len(s.scores[chat]) == 0 {
			delete(s.scores, chat)
		}
	}
}
