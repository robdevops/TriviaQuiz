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
)

// usage is the help for /quiz.
const usage = "Try /quiz, /quiz 80s music, /quiz space 5 45s, /quiz end or /quiz scores."

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

// botSampleTimeout is how long the bot works on a sampling request, even
// one the quiz has given up on; it takes no other meanwhile.
const botSampleTimeout = 2 * time.Minute

// tools are the quiz's tools, both for the bot alone: neither is offered
// to the model.
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

// busy is the answer to /quiz while a quiz is on.
func busy(q *quiz) addon.Result {
	return addon.Text(fmt.Sprintf("A quiz is on: question %d of %d.", q.Current+1, len(q.Questions)))
}

// start has the model write a quiz for c's chat and begins it, answering
// with its first question and the watch that takes the replies to it.
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
	select {
	case s.slot <- struct{}{}:
	default:
		select {
		case s.slot <- struct{}{}:
		case <-gctx.Done():
			return addon.Text(slotBusy)
		}
	}
	began := time.Now()
	qs, st, err := s.write(gctx, c.Chat, topic, n)
	if hold := st.lastSent.Add(botSampleTimeout).Sub(s.clock.Now()); st.cancelled && hold > 0 {
		// The bot works on the cancelled request until its own timeout.
		s.clock.AfterFunc(hold, func() { <-s.slot })
	} else {
		<-s.slot
	}
	took := time.Since(began).Round(100 * time.Millisecond)
	if err != nil {
		s.log.Printf("no quiz: %v, after %d sampling rounds in %s, %d rejected, %d repeats", err, st.rounds, took, st.rejected, st.repeats)
		switch {
		case errors.Is(err, errTooSlow):
			return addon.Text(tooSlow)
		case errors.Is(err, errBusy):
			return addon.Text(slotBusy)
		case errors.Is(err, errRefused):
			return addon.Text(refused)
		case errors.Is(err, errPoor):
			return addon.Text(poor)
		}
		return addon.Text(failed)
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
		ID: "q" + randomID(6), Chat: c.Chat, Starter: c.Sender, Topic: topic,
		Questions: qs, Time: length,
		Started: now, End: now.Add(time.Duration(len(qs))*(length+settle) + time.Duration(len(qs)-1)*s.gap),
		Opened: now, Deadline: now.Add(length),
		Answers: map[string]answer{}, Points: map[string]int{}, Right: map[string]int{},
	}
	s.quizzes[c.Chat] = q
	s.remember(c.Chat, qs, now)
	if c.Group && s.srv.WatchReactions() { // players join by reacting to the intro first
		q.Lobby, q.Joined, q.CallOff = true, nil, now.Add(lobbyMax)
		q.End = q.End.Add(lobbyMax)
		s.armLobby(q)
	} else {
		if !c.Group { // a DM: its one player, so no question waits for the timer
			q.Players = []string{c.Sender}
		}
		s.arm(q)
		// The question is a post of its own, so its answer can reply to it. It
		// waits in the chat's mailbox behind this call's answer, the intro, so
		// it always comes second, once the watch that takes its replies is on.
		q.Post = s.queue(addon.Op{Op: "post", Chat: q.Chat, Text: clamp(questionText(q), maxPost)})[0]
	}
	text := intro(q)
	s.mu.Unlock()
	s.save()
	s.log.Printf("quiz %s started: %d questions of %s, %d sampling rounds in %s, %d rejected, %d repeats",
		q.ID, len(qs), length, st.rounds, took, st.rejected, st.repeats)
	return addon.Result{
		Content: addon.Text(clamp(text, maxPost)).Content,
		Meta: &addon.ResultMeta{Watch: &addon.Watch{
			ID: q.ID, Replies: true, Reactions: q.Lobby, Until: q.End.Add(watchSlack).Format(time.RFC3339), Tool: "quiz_answer", Over: "That quiz is over. Start another with /quiz.",
		}},
	}
}

// intro is the quiz's first lines: its topic, its length, and how to
// answer.
func intro(q *quiz) string {
	title := "🧠 **Quiz**"
	if q.Topic != "" {
		title = "🧠 **Quiz: " + q.Topic + "**"
	}
	how := "Answer by **replying** to the quiz's messages: the letter for a multiple-choice question, or the answer itself. Your last answer before time is up counts: edit it, or reply again, to change it. "
	if q.Lobby {
		return fmt.Sprintf("%s · %d %s, %s each.\n👋 **React to this message with any emoji to join**, whoever started it too: the quiz starts 30 seconds after the last player joins, once at least 2 have.\n%sA question ends as soon as everyone who joined has answered.",
			title, len(q.Questions), plural(len(q.Questions), "question", "questions"), lengthLabel(q.Time), how)
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

// arm sets q's timer to reveal its open question once its time is up.
// s.mu is held.
func (s *server) arm(q *quiz) {
	chat, id, current := q.Chat, q.ID, q.Current
	if t := s.timers[chat]; t != nil {
		t.Stop()
	}
	s.timers[chat] = s.clock.AfterFunc(q.Deadline.Add(settle).Sub(s.clock.Now()), func() { s.timeUp(chat, id, current) })
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
		return addon.Text("The quiz is off.")
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
	for _, m := range []map[string]stopper{s.timers, s.asks} {
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
		q.Joined = append(q.Joined, c.Sender)
		q.LastJoin = s.clock.Now()
	case removed && in:
		q.Joined = slices.DeleteFunc(q.Joined, func(u string) bool { return u == c.Sender })
	default:
		s.mu.Unlock()
		return addon.Text("Noted.")
	}
	s.armLobby(q)
	s.mu.Unlock()
	s.save()
	return addon.Text("Noted.")
}

// armLobby sets q's lobby timer: with enough players, to start lobbyWait
// after the last join; else to call the quiz off at CallOff. s.mu is held.
func (s *server) armLobby(q *quiz) {
	at := q.CallOff
	if len(q.Joined) >= minPlayers {
		at = q.LastJoin.Add(lobbyWait)
	}
	chat, id := q.Chat, q.ID
	if t := s.timers[chat]; t != nil {
		t.Stop()
	}
	s.timers[chat] = s.clock.AfterFunc(max(at.Sub(s.clock.Now()), 0), func() {
		s.mu.Lock()
		q := s.quizzes[chat]
		if q == nil || q.ID != id || !q.Lobby {
			s.mu.Unlock()
			return
		}
		now := s.clock.Now()
		switch {
		case len(q.Joined) >= minPlayers && !now.Before(q.LastJoin.Add(lobbyWait)):
			s.beginLocked(q)
		case len(q.Joined) < minPlayers && !now.Before(q.CallOff):
			s.callOffLocked(q, "Not enough players joined, so the quiz is off.")
		default:
			s.armLobby(q)
		}
		s.mu.Unlock()
		s.save()
	})
}

// beginLocked ends q's lobby: its players are those who joined, and
// question 1 opens now. s.mu is held.
func (s *server) beginLocked(q *quiz) {
	now := s.clock.Now()
	q.Lobby, q.Players = false, slices.Clone(q.Joined)
	q.Started, q.Opened, q.Deadline = now, now, now.Add(q.Time)
	s.arm(q)
	s.askLocked(q)
	s.log.Printf("quiz %s began: %d players", q.ID, len(q.Players))
}

// callOffLocked ends q in its lobby, saying why if why isn't "", and ends
// its watch. s.mu is held.
func (s *server) callOffLocked(q *quiz, why string) {
	if why != "" {
		s.queue(addon.Op{Op: "post", Chat: q.Chat, Text: why})
	}
	s.queue(addon.Op{Op: "unwatch", Chat: q.Chat, Watch: q.ID})
	delete(s.quizzes, q.Chat)
	for _, m := range []map[string]stopper{s.timers, s.asks} {
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
