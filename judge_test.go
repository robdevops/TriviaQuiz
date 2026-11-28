package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

// textQuiz starts a one-question quiz on peak, a medium text question of
// 1 minute, and returns its first page of ops: the question.
func textQuiz(t *testing.T) (*fakeClock, *client, *fakeModel, *syncBuffer, outboxPage) {
	t.Helper()
	clk, c, model, logs := begin(t)
	model.add(asJSON(peak))
	c.quiz("1 1m", from(clk, chat, alice))
	c.note()
	page := c.outbox("0")
	if len(page.Ops) != 1 {
		t.Fatalf("the question %+v", page)
	}
	return clk, c, model, logs, page
}

// judgedItems is the answers a marking request sends, in order.
func judgedItems(t *testing.T, req sampleRequest) (map[string]any, []string) {
	t.Helper()
	if req.SystemPrompt != judgeSystem || len(req.Messages) != 1 || req.Messages[0].Role != "user" {
		t.Fatalf("marking request %+v", req)
	}
	var body struct {
		Question string   `json:"question"`
		Accepted []string `json:"accepted"`
		Answers  []struct {
			ID   int    `json:"id"`
			Text string `json:"text"`
		} `json:"answers"`
	}
	text := req.Messages[0].Content.Text
	if err := json.Unmarshal([]byte(text), &body); err != nil {
		t.Fatalf("marking request %q: %v", text, err)
	}
	var all map[string]any
	json.Unmarshal([]byte(text), &all)
	var items []string
	for i, a := range body.Answers {
		if a.ID != i+1 {
			t.Errorf("answer %d has id %d", i+1, a.ID)
		}
		items = append(items, a.Text)
	}
	if body.Question != "What is the highest mountain on Earth?" || !slices.Equal(body.Accepted, []string{"Everest", "Mount Everest"}) {
		t.Errorf("marking request %q", text)
	}
	return all, items
}

// The model marks the answers the code rejected: one it accepts scores,
// with the ⚡ if it was the first; the code's right answers stay right, and
// the request names nobody.
func TestJudged(t *testing.T) {
	clk, c, model, logs, page := textQuiz(t)
	clk.Advance(3 * time.Second)
	c.reply("Chomolungma", from(clk, chat, bob)) // its Tibetan name: the code rejects it
	clk.Advance(2 * time.Second)
	c.reply("mount everest", from(clk, chat, alice))
	clk.Advance(time.Second)
	c.reply("Mt Evrest", from(clk, chat, "u_carol"))
	clk.Advance(time.Second)
	c.reply("K2", from(clk, chat, "u_dave"))
	// Carol's is right too, Dave's isn't; an id out of range and a verdict
	// against nothing are ignored.
	model.add("Here you go:\n```json\n" + `{"answers":[{"id":1,"right":true},{"id":2,"right":true},{"id":3,"right":false},{"id":9,"right":true},{"id":0,"right":true}]}` + "\n```")
	clk.Advance(53*time.Second + settle)
	page = waitOps(t, c, page.Cursor, 4) // ⏰, the answer, the results and the unwatch
	reqs := model.sent()
	if len(reqs) != 2 {
		t.Fatalf("%d requests", len(reqs))
	}
	all, items := judgedItems(t, reqs[1])
	if !slices.Equal(items, []string{"Chomolungma", "Mt Evrest", "K2"}) {
		t.Errorf("judged %q", items)
	}
	if len(all) != 3 {
		t.Errorf("the request has more than the question, its answers and the replies: %v", all)
	}
	for _, name := range []string{alice, bob, "u_carol", "u_dave", "u_"} {
		if strings.Contains(reqs[1].Messages[0].Content.Text, name) {
			t.Errorf("the request names %s: %q", name, reqs[1].Messages[0].Content.Text)
		}
	}
	reveal, results := page.Ops[1].Text, page.Ops[2].Text
	if !strings.HasSuffix(reveal, "Right: {u_bob} ⚡, {u_alice}, {u_carol}") {
		t.Errorf("reveal %q", reveal)
	}
	if !strings.Contains(results, "🥇 {u_bob} 15") || strings.Contains(results, "u_dave} 1") {
		t.Errorf("results %q", results)
	}
	if log := logs.String(); strings.Contains(log, "Chomolungma") || strings.Contains(log, "K2") {
		t.Errorf("the log has the answers:\n%s", log)
	}
}

// While the model marks, the answers are fixed: a late one isn't
// recorded, and the warning isn't sent.
func TestJudgingFixesAnswers(t *testing.T) {
	clk, c, model, _, page := textQuiz(t)
	clk.Advance(5 * time.Second)
	c.reply("Everst", from(clk, chat, bob)) // within the code's slip: right
	c.reply("Chomolungma", from(clk, chat, alice))
	gate := make(chan struct{})
	model.mu.Lock()
	model.gate = gate
	model.mu.Unlock()
	model.add(`{"answers":[{"id":1,"right":true}]}`)
	clk.Advance(55*time.Second + settle)
	waitFor(t, "a marking request", func() bool { return len(model.sent()) == 2 })
	late := from(clk, chat, "u_carol")
	late.Sent = clk.Now().Add(-settle).Unix() // sent before the deadline, arriving while the model marks
	if r := c.reply("Everest", late); recorded(r) {
		t.Errorf("an answer while judging: %+v", r)
	}
	close(gate)
	page = waitOps(t, c, page.Cursor, 4)
	if !strings.HasSuffix(page.Ops[1].Text, "Right: {u_bob} ⚡, {u_alice}") {
		t.Errorf("reveal %q", page.Ops[1].Text)
	}
}

// The model can't turn an answer the code accepted wrong; junk, a refusal,
// an error or a timeout leaves the code's marks.
func TestJudgeFallsBack(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*client, *fakeModel)
	}{
		{"wrong verdicts", func(_ *client, m *fakeModel) {
			m.add(`{"answers":[{"id":1,"right":false},{"id":2,"right":false},{"id":3,"right":false}]}`)
		}},
		{"junk", func(_ *client, m *fakeModel) { m.add("Both look right to me!") }},
		{"malformed", func(_ *client, m *fakeModel) { m.add(`{"answers":[{"id":"one"}]}`) }},
		{"error", func(*client, *fakeModel) {}}, // the fake has no reply left
		{"refused", func(_ *client, m *fakeModel) {
			m.mu.Lock()
			m.refuse = true
			m.mu.Unlock()
		}},
		{"busy", func(c *client, _ *fakeModel) { // another chat's quiz is being written
			c.s.mu.Lock()
			c.s.judgeIn = 100 * time.Millisecond
			c.s.mu.Unlock()
			c.s.slot <- struct{}{}
		}},
		{"timeout", func(c *client, m *fakeModel) {
			c.s.mu.Lock()
			c.s.judgeIn = 100 * time.Millisecond
			c.s.mu.Unlock()
			m.mu.Lock()
			m.hang = true
			m.mu.Unlock()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clk, c, model, logs, page := textQuiz(t)
			clk.Advance(5 * time.Second)
			c.reply("Chomolungma", from(clk, chat, bob))
			c.reply("Everest", from(clk, chat, alice))
			tc.setup(c, model)
			clk.Advance(55*time.Second + settle)
			page = waitOps(t, c, page.Cursor, 4)
			if !strings.HasSuffix(page.Ops[1].Text, "Right: {u_alice} ⚡") {
				t.Errorf("reveal %q", page.Ops[1].Text)
			}
			if tc.name != "wrong verdicts" && !strings.Contains(logs.String(), "marking failed, the code's marks stand") {
				t.Errorf("log:\n%s", logs)
			}
		})
	}
}

// No request when the code accepted every answer, or no answers came, or
// for a choice question.
func TestNoJudging(t *testing.T) {
	clk, c, model, _, page := textQuiz(t)
	clk.Advance(5 * time.Second)
	c.reply("Everest", from(clk, chat, alice))
	c.reply("Mount Everest", from(clk, chat, bob))
	clk.Advance(55*time.Second + settle)
	c.note()
	if p := c.outbox(page.Cursor); len(p.Ops) != 4 {
		t.Fatalf("the reveal %+v", p)
	}
	if n := len(model.sent()); n != 1 {
		t.Errorf("%d requests", n)
	}

	clk, c, model, _ = begin(t)
	model.add(asJSON(capital, peak))
	c.quiz("2 1m", from(clk, chat, alice))
	c.note()
	page = c.outbox("0")
	clk.Advance(5 * time.Second)
	c.reply(wrongLetter(c), from(clk, chat, bob))
	clk.Advance(55*time.Second + settle)
	c.note()
	page = c.outbox(page.Cursor)
	if len(page.Ops) != 3 || !strings.Contains(page.Ops[1].Text, "Nobody got it.") { // ⏰, the answer and question 2
		t.Fatalf("the choice reveal %+v", page)
	}
	clk.Advance(60*time.Second + settle) // question 2, unanswered
	c.note()
	if p := c.outbox(page.Cursor); len(p.Ops) != 4 {
		t.Fatalf("question 2's reveal %+v", p)
	}
	if n := len(model.sent()); n != 1 {
		t.Errorf("%d requests", n)
	}
}

// Without the model, after a restart on a bot that no longer offers it,
// the code's marks stand.
func TestNoJudgingWithoutModel(t *testing.T) {
	clk := &fakeClock{now: time.Date(2026, 11, 20, 14, 0, 0, 0, time.UTC)}
	dir := t.TempDir()
	var logs syncBuffer
	model := &fakeModel{}
	model.add(asJSON(peak))
	c := connect(t, testServer(t, dir, clk, &logs), model)
	c.initialize(true, true)
	c.quiz("1 1m", from(clk, chat, alice))
	c.close()
	c = connect(t, testServer(t, dir, clk, &logs), nil)
	c.initialize(false, false)
	clk.Advance(5 * time.Second)
	c.reply("Chomolungma", from(clk, chat, bob))
	c.reply("Everest", from(clk, chat, alice))
	clk.Advance(55*time.Second + settle)
	c.note()
	if r := revealIn(c.outbox("0").Ops); !strings.HasSuffix(r, "Right: {u_alice} ⚡") || strings.Contains(r, " | ") {
		t.Fatalf("reveal %q", r)
	}
}

// A verdict for a question that has moved on is ignored: one ended by
// /quiz end, or by a restart, which reveals it with the code's marks.
func TestJudgeMovedOn(t *testing.T) {
	t.Run("ended", func(t *testing.T) {
		clk, c, model, _, page := textQuiz(t)
		clk.Advance(5 * time.Second)
		c.reply("Chomolungma", from(clk, chat, bob))
		gate := make(chan struct{})
		model.mu.Lock()
		model.gate = gate
		model.mu.Unlock()
		model.add(`{"answers":[{"id":1,"right":true}]}`)
		clk.Advance(55*time.Second + settle)
		waitFor(t, "a marking request", func() bool { return len(model.sent()) == 2 })
		page = c.outbox(page.Cursor) // the ⏰
		if r := words(c.quiz("end", from(clk, chat, alice))); !strings.Contains(r, "Nobody got it.") {
			t.Errorf("/quiz end %q", r)
		}
		close(gate)
		time.Sleep(50 * time.Millisecond)
		c.note()
		if p := c.outbox(page.Cursor); len(p.Ops) != 1 || p.Ops[0].Op != "unwatch" {
			t.Errorf("after the verdict %+v", p)
		}
	})
	t.Run("restart", func(t *testing.T) {
		clk := &fakeClock{now: time.Date(2026, 11, 20, 14, 0, 0, 0, time.UTC)}
		dir := t.TempDir()
		var logs syncBuffer
		model := &fakeModel{}
		model.add(asJSON(peak))
		c := connect(t, testServer(t, dir, clk, &logs), model)
		c.initialize(true, true)
		c.quiz("1 1m", from(clk, chat, alice))
		clk.Advance(5 * time.Second)
		c.reply("Chomolungma", from(clk, chat, bob))
		c.reply("Everest", from(clk, chat, alice))
		model.mu.Lock()
		model.hang = true
		model.mu.Unlock()
		clk.Advance(55*time.Second + settle)
		waitFor(t, "a marking request", func() bool { return len(model.sent()) == 2 })
		c.close()

		c = connect(t, testServer(t, dir, clk, &logs), nil)
		c.initialize(false, false)
		c.note()
		if r := revealIn(c.outbox("0").Ops); !strings.HasSuffix(r, "Right: {u_alice} ⚡") || strings.Contains(r, " | ") {
			t.Fatalf("after the restart %q", r)
		}
	})
}

// revealIn is the text of the one reveal among ops.
func revealIn(ops []outboxOp) string {
	var found []string
	for _, o := range ops {
		if strings.HasPrefix(o.Text, "✅") {
			found = append(found, o.Text)
		}
	}
	return strings.Join(found, " | ")
}
