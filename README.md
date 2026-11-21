# Quiz

A trivia quiz for [signalbot](https://github.com/robdevops/signalbot). `/quiz`
has the bot's own model write a few questions on a topic, some multiple
choice (A–D) and some answered in a word or two. Players answer each
question by **replying** to the quiz's messages; when a question's time is
up the bot gives the answer, why it is right, who got it, and the next
question. After the last, it ranks the players, and keeps a 7-day
scoreboard.

```
/quiz                      a quiz of general knowledge (QUIZ_QUESTIONS questions of QUIZ_TIME, default 5 of 30s)
/quiz 80s music            a quiz on a topic, up to 60 characters
/quiz 80s music 5 45s      with a number of questions (1 to 10) and a time for each (10s to 2m), at the end, in either order
/quiz end                  finish now: the open question's answer and the results (only the player who started it)
/quiz scores               this chat's totals over the last 7 days
```

It is a separate program that signalbot starts as an
[MCP addon](https://github.com/robdevops/signalbot/blob/main/docs/FILES_LINKS_TOOLS_MCP.md#addons) server. It speaks MCP
2025-06-18 over stdio (newline-delimited JSON-RPC), with signalbot's addon
extension, through signalbot's own Go package for addons,
[`github.com/robdevops/signalbot/addon`](https://github.com/robdevops/signalbot/tree/main/addon),
and no other MCP library. It holds no API key: it asks the bot's model
through MCP's sampling request, and the bot runs it with its own key and
counts the cost against the chat's `DAILY_BUDGET`.

## How to play

- **Always answer by replying** to one of the quiz's messages: the first
  question, or any post since. That works on every network, and on
  Telegram whatever the bot's privacy mode, since a reply to the bot always
  reaches it. A reply to any of the quiz's messages counts for the question
  that is open.
- **Multiple choice:** reply with the letter, `A`, `B`, `C` or `D` (`b`,
  `B)` and `(b)` work too). Any other reply to a multiple-choice question,
  such as "lol, hard one", isn't an answer and doesn't use up yours.
- **Free text:** reply with the answer. Case, accents, punctuation and
  "a", "an" and "the" don't matter, and a small slip is forgiven: one letter
  in an answer of 5 to 9 letters, two in one of 10 or more. Numbers must be
  exact.
- **Your first answer counts.** Changing it, or editing the message, does
  nothing. Answers get no reaction; the reveal says who got it right.
- Messages that aren't replies to the quiz are left alone, so the chat
  carries on around it.
- **Points:** 5 for an easy question, 10 for a medium one, 15 for a hard
  one, and 5 more for the first right answer (marked ⚡).

## Install

It needs a signalbot with MCP sampling and reply watches (the bot offers
both in `initialize`; an older bot, or one without a model, gets "The quiz
needs the bot's model: give the quiz addon the model permission."). Get the
code and build it with Go 1.27 or newer, as a static binary, which the
Docker image needs (it has no shell or runtimes) and systemd is happy with:

```
git clone https://github.com/robdevops/chat-plugin-quiz.git
cd chat-plugin-quiz
CGO_ENABLED=0 go build -ldflags='-s -w' -o /opt/signalbot-addons/quiz .
```

The build downloads signalbot's module, at the commit `go.mod` names, for
its `addon` package; only that package, which needs nothing beyond Go's
standard library, is built into the quiz.

Mount it read-only: under Docker, add `- /opt/signalbot-addons:/addons:ro` to
the volumes in `compose.yaml`; under systemd, use the host path, or bind it
with `BindReadOnlyPaths=/opt/signalbot-addons:/addons` in a drop-in. Then add
it to the MCP servers file (`MCP_CONFIG`). The server must be called `quiz`,
which is what gives it the `QUIZ_` settings below:

```jsonc
"quiz": {
  "command": "/addons/quiz",
  "chat_commands": {"quiz": {"tool": "quiz", "direct": true, "example": "/quiz 80s music", "timeout": "90s"}},
  "addon": {
    "chats": ["<group id>"],
    "post": true, "watch": true,
    "model": {"per_hour": 30, "max_tokens": 4000}
  }
}
```

`chats` takes the groups' IDs as Signal or Telegram shows them, or `["all"]`;
add `"dms": true` to play in DMs too. The quiz needs:

- `model`, to have the bot's model write the questions: one request per
  quiz, and at most 2 more when some questions come back unusable. Keep
  `max_tokens` at or above `QUIZ_MAX_TOKENS` (3000 by default); the bot caps
  each answer at it;
- `post`, for each answer and the next question, and the results. The bot
  drops a post over the chat's post `rate` for good, and a reveal it drops
  holds the next question, so the quiz asks for no reactions, which take
  from the same budget: one post per question fits the default rate;
- `watch`, to read the replies. A quiz is at most 10 questions of 2 minutes,
  and never over 30 minutes, so the default `watch_max` of 1h is enough.

`"timeout": "90s"` gives the model time to write the quiz. The quiz gives up
a few seconds before the bot's timeout, says so ("The bot's model didn't
write the quiz in time…"), and starts no quiz the bot has given up on. The
bot takes one sampling request at a time from an addon, so the quiz writes
one quiz at a time: a `/quiz` in a second chat meanwhile waits for it. The
bot works on a request the quiz gave up on for up to 2 minutes, and the
next quiz waits for that too, or is told to try again in a minute.

The quiz keeps its own files in `QUIZ_STATE_DIR`, which you create, writable
by the bot's user, and set in `/etc/signalbot.env`:

- **systemd:** in a drop-in (`sudo systemctl edit signalbot`), add
  `StateDirectory=quiz` (the unit's `DynamicUser=` makes it and owns it),
  and set `QUIZ_STATE_DIR=/var/lib/quiz`.
- **Docker:** `sudo install -d -o 65532 -g 65532 -m 700 /var/lib/private/quiz`,
  add `- /var/lib/private/quiz:/quiz` to the volumes, and set
  `QUIZ_STATE_DIR=/quiz`.

It shares the bot's memory limit, so allow for `QUIZ_MEMLIMIT` in
`mem_limit` or `MemoryMax`.

## Settings

signalbot passes an addon server called `quiz` every `QUIZ_` variable from
its environment file, plus `PATH`, `HOME` and the bot's proxy and time zone
settings, and nothing else.

| Variable | Default | Description |
|---|---|---|
| `QUIZ_STATE_DIR` | — (required) | Where the quiz keeps `quiz.json` and its log. Create it, writable by the bot's user; the quiz exits at start if it can't list and write it |
| `QUIZ_QUESTIONS` | `5` | A quiz's questions when `/quiz` doesn't say, 1 to 10 |
| `QUIZ_TIME` | `30s` | The time for each question when `/quiz` doesn't say, 10s to 2m |
| `QUIZ_LANGUAGE` | `English` | The language the model writes the questions and answers in, by name (letters, spaces and hyphens, up to 40) |
| `QUIZ_DIFFICULTY` | `mixed` | `mixed` (about a third each of easy, medium and hard), or `easy`, `medium` or `hard` for every question |
| `QUIZ_MAX_TOKENS` | `3000` | The most the model may write for one quiz, 500 to 32000; the addon block's `max_tokens` caps it too |
| `QUIZ_MEMLIMIT` | `64MiB` | Memory limit, applied with `debug.SetMemoryLimit`, written as `GOMEMLIMIT` is (`B`, `KiB`, `MiB`, `GiB`, `TiB`) |

## Keeping the questions good

Bad questions are the weak point of LLM trivia, so the prompt sets rules and
the quiz checks what comes back.

- The system prompt asks for exactly one defensible answer (no opinions,
  rankings or trick wording, and plausible but definitely wrong choices);
  nothing that may have changed since the model's training (records,
  "current" anything, recent events), since sampling has no web search to
  check a fact; an explanation for every question, shown with the answer;
  a mix of difficulties and of the two kinds; free-text answers of one to
  three words, with their accepted variants and numbers as digits; and
  `QUIZ_LANGUAGE`. The topic is a player's text, and the prompt says to
  treat it as a topic only.
- The quiz shuffles every question's choices, as models put the answer
  first far too often.
- It rejects JSON that isn't strict (once code fences are stripped), a
  question without exactly 4 different choices, an answer that isn't among
  them, a free-text question with no accepted answers, a missing
  explanation or difficulty, a link, and over-long text (a question or an
  explanation over 300 characters, a choice over 80).
- It keeps a hash of each question asked in a chat for 30 days, and rejects
  a question asked there lately.
- When some questions are rejected, it asks the model again in the same
  conversation, naming what was wrong ("question 3 repeats one asked in this
  chat recently; question 5 has an answer that isn't among its choices.
  Write 2 replacements…"). After at most 2 such rounds it plays with the
  good questions if there are at least 3 (or all of a shorter quiz), and
  otherwise says it couldn't write a good quiz on that topic.
- What the model writes is only ever posted as text, on one line each, with
  Markdown's characters and `{…}` taken out, so it can't style a post, link,
  or name anyone.

## Privacy

The quiz gets only what signalbot's addon block allows: the text after
`/quiz`, and during a quiz the replies to its own messages, each with the
chat and sender as pseudonyms and the time the bot got it. It never sees a
phone number, account ID, group ID or, unless `names` is set, a display
name; it writes players as `{u_…}` and the bot puts their names in.
Replies are claimed by the bot, so they are never answered, sent to the
model or kept in the chat's history.

The topic and the quiz's prompt go to the bot's model, and so to its
provider, as chat does when someone asks the bot something. Nothing a
player replies goes to the model: answers are marked by the quiz itself.

### What it keeps

`$QUIZ_STATE_DIR/quiz.json`, rewritten whole on each change:

- open quizzes: the chat's pseudonym, who started it, the topic, the
  questions with their answers and explanations, the open question's
  deadline, and for each player (a per-chat pseudonym) only whether their
  answer to the open question was right and when they sent it, and their
  points so far. What anyone replied is never kept, in memory or on disk;
- 7 days of points per chat, by player pseudonym;
- 30 days of hashes of the questions asked in each chat;
- outbox ops the bot hasn't read yet, for at most an hour.

`$QUIZ_STATE_DIR/quiz.log` (moved to `quiz.log.1` past 4 MiB at start)
records quizzes started and ended, with the number of questions, sampling
rounds, rejected questions and repeats, times, and how many players scored.
It never holds a question, an answer, a topic, a prompt, a name, a
pseudonym or message text.

## The protocol

The `addon` package speaks the protocol: the handshake, tool calls and
their cancellation, sampling the bot's model, and the outbox the bot reads.
The quiz adds:

- `quiz` (`{args}`, a direct command) and `quiz_answer` (`{text}`, the
  watch's tool), both for the bot alone, so neither is offered to the model.
- `/quiz` checks that the bot offered `sampling` and, in the extension's
  capability, `watchReplies`. It then sends one `sampling/createMessage`,
  charged to the command's chat (`_meta["io.github.robdevops.signalbot/chat"]`),
  and at most 2 follow-ups in the same conversation, all before the call's
  `deadline`, less 3s to reply.
- It answers with the first question and one watch for the whole quiz:
  `replies: true`, no pattern, `tool: "quiz_answer"`, until 10s past the
  quiz's end. No answer's result asks the bot to react, as reactions take
  from the chat's post rate, and a post over it is lost.
- When a question's time is up (3s after its deadline, for answers still in
  the bot's queue), it queues one `post`: the answer and explanation, who
  got it right as `{u_…}` placeholders, and the next question. After the
  last, the post holds the results instead, followed by an `unwatch`.
  `/quiz end` answers with the open question's answer and the results itself
  and queues only the `unwatch`. Every post stays under the bot's 4000
  characters.
- Ops the bot hasn't read are saved in `quiz.json`, with their IDs, so they
  survive a restart. On start, quizzes still open get their timers back, and
  a question whose time passed while the quiz was down is revealed at once;
  the next one gets what is left of the watch, and the quiz ends early if
  too little is.
