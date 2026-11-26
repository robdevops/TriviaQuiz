# TriviaQuiz: details

The fine print behind the [README](../README.md): how the quiz works, how
to set it up, and what it keeps.

## How it runs

It is a separate program that signalbot starts as an
[MCP addon](https://github.com/robdevops/signalbot/blob/main/docs/FILES_LINKS_TOOLS_MCP.md#addons) server. It speaks MCP
2025-06-18 over stdio (newline-delimited JSON-RPC), with signalbot's addon
extension, through signalbot's own Go package for addons,
[`github.com/robdevops/signalbot/addon`](https://github.com/robdevops/signalbot/tree/main/addon),
and no other MCP library. It holds no API key: it asks the bot's model
through MCP's sampling request, and the bot runs it with its own key and
counts the cost against the chat's `DAILY_BUDGET`.

## Playing, in full

- **Always answer by replying** to one of the quiz's messages: the first
  question, or any post since. That works on every network, and on
  Telegram whatever the bot's privacy mode, since a reply to the bot always
  reaches it. A reply to any of the quiz's messages counts for the question
  that is open. In a DM, every message counts as an answer, reply or not.
- **Multiple choice:** reply with the letter, `A`, `B`, `C` or `D` (`b`,
  `B)` and `(b)` work too). Any other reply to a multiple-choice question,
  such as "lol, hard one", isn't an answer and doesn't use up yours.
- **Free text:** reply with the answer. Case, accents, punctuation and
  "a", "an" and "the" don't matter, and a small slip is forgiven: one letter
  in an answer of 5 to 9 letters, two in one of 10 or more. Numbers must be
  exact.
- **Your last answer before time is up counts.** To change it, edit your
  reply, or reply again (deleting the old one first if you like: a deleted
  reply that isn't replaced still counts, as the bot doesn't tell addons of
  deletes). Answers get no reaction, and the reveal says who got it right,
  so changing one gives nothing away.
- **5 seconds left.** The bot reacts ⏰ to a question with 5 seconds left
  (on Telegram 😱, as Telegram takes only some emoji as reactions, and not
  ⏰), unless it has already ended (everyone answered, or `/quiz end`).
- **Joining.** In a group, the quiz starts with a lobby: react to its intro
  with any emoji to join (take the reaction back to leave), or, where the
  bot shows buttons, press ✅ (Ready) under it, which the intro then asks for
  instead ("You're in: 2 joined.", or "You're already in."; reactions still
  count). ✅ and a reaction are the same join, one per player. Whoever
  starts it must join too. Without buttons it starts 30 seconds
  after the last player joins, once there are at least 2 of you, and is
  called off if fewer than 2 have joined after 15 minutes ("Not enough
  players joined, so the quiz is off."). With buttons there is no
  countdown: only the starter starts it, and it is called off after 15
  minutes all the same (with 2 or more joined, "Nobody pressed ▶️, so
  the quiz is off."). Those who joined
  are the players every question waits for; anyone may still answer. The
  intro comes at once, and the model writes the questions while players
  join. If the lobby is ready first, the bot posts "🧠 Writing the
  questions…" and starts as soon as they are written (if players left
  meanwhile, it waits for 2 again); if they can't be written within 2
  minutes, it says why and the quiz is off. A lobby called off while its
  questions are written leaves the model busy with them for up to 2
  minutes, so a new group quiz meanwhile is turned down with "try again". On Telegram
  the bot sees reactions only as the group's admin. In a DM there is no
  lobby: the questions are written first, then the quiz starts at once.
- **The starter's controls.** In a lobby, the player who started the quiz
  can start it now (`/quiz start`, or ▶️), which counts as their join
  and needs 2 joined ("Waiting for at least 2 players: react to join.", or
  "press ✅ to join." where the bot shows buttons), and goes exactly
  as when the countdown runs out (waiting for the questions if they aren't
  written yet); call it off (`/quiz stop` or `end`, or ✖️, which
  posts "The quiz is off."); or, without buttons, put the start off by 30
  seconds (`/quiz wait`), as often as they like, for later countdowns too,
  up to the 15-minute limit ("Can't wait any longer."). With buttons,
  `/quiz wait` answers that there's no countdown.
- **The lobby's buttons.** Where the bot shows buttons (not on Signal),
  the intro has one row, with no other words or emoji: `✅ N` (Ready),
  anyone's, with how many joined; the starter's setting, `Rounds: N`; ▶️
  (Start), once 2 have joined (it goes again if they drop below 2); and ✖️
  (Cancel). The intro then says to press ✅ to join, that the starter
  presses ▶️ when everyone's in (✖️ calls it off) and can change the
  number of rounds first; its first line shows the number of questions
  and each one's time, as without buttons; each question's time is the command's (or
  `QUIZ_TIME`'s), with no button. The setting has two modes: what the
  quiz was started with (the default or the command's) and double that,
  up to the game's limit of 10; a press switches between them, its pop-up
  the new value ("Rounds: 10"), and one started at the limit answers
  "Already the most.". More questions than are written are written in the
  background, and fewer just plays the first ones; it can't change once
  ▶️ is waiting for the questions. Every join, leave or setting
  replaces the row under the intro; a change of the setting rewrites the
  intro too, with the new number of questions ("5 questions" becomes
  "10 questions"). Anyone else pressing one of the
  starter's gets "Only the player who started this quiz can do that.",
  and a press once the lobby is over, ✅'s too, gets "That quiz has
  already started." or "That quiz is over.". A second `/quiz` in the
  lobby gets "A quiz is waiting for players: react to its message to
  join, or /quiz start." (with buttons: "press ✅ under its message
  to join.")
- **A pause to read the answer.** When a question ends, its answer comes as
  a reply to it, and the next question 10 seconds later, with its full time.
- **No waiting once everyone's in.** After a lobby, every question ends as
  soon as all who joined have answered (3s later, for answers still on
  their way). Without one, question 1 runs its full time, whoever answers
  it is playing, and from question 2 on a question ends once all of them
  have answered. In a DM, a question ends as soon as you answer. So a quiz
  goes as fast as its players. Someone who joins later still scores, but
  isn't waited for; a player who stops answering just means the timer runs.
- Messages that aren't replies to the quiz are left alone, so the chat
  carries on around it.
- **After the quiz** (at the end, by `/quiz end` or `stop`, or called off),
  for an hour a reply to one of its messages gets "That quiz is over. Start
  another with /quiz." instead of the bot's model. The bot keeps this in
  memory only, so a restart forgets it.
- **Points:** 5 for an easy question, 10 for a medium one, 15 for a hard
  one, and 5 more for the first right answer (marked ⚡). The model is asked
  for medium questions unless `QUIZ_DIFFICULTY` says otherwise.

## Install notes

It needs signalbot at c755fc7 or newer, for MCP sampling, reply and
reaction watches, and reactions to its own posts; f6a3c5c or newer for
the lobby's buttons, which it updates with the outbox's `buttons` op. A bot without a model,
or without the model permission, gets "The quiz needs the bot's model:
give the quiz addon the model permission." Get the code and build it
with Go 1.27 or newer, as a static binary, which the Docker image needs
(it has no shell or runtimes) and systemd is happy with:

```
git clone https://github.com/robdevops/TriviaQuiz.git
cd TriviaQuiz
CGO_ENABLED=0 go build -ldflags='-s -w' -o /tmp/quiz . && sudo install -D -m 755 /tmp/quiz /opt/signalbot-addons/quiz
```

The build downloads signalbot's module, at the commit `go.mod` names, for
its `addon` package; only that package, which needs nothing beyond Go's
standard library, is built into the quiz.

Give its full path as `command`. Under systemd there's nothing to mount:
the unit's `ProtectSystem=strict` leaves `/opt` readable. Under Docker,
mount the directory read-only at the same path, by adding
`- /opt/signalbot-addons:/opt/signalbot-addons:ro` to the volumes in
`compose.yaml`, so the same file works for both. Then add
it to the MCP servers file (`MCP_CONFIG`). The server must be called `quiz`,
which is what gives it the `QUIZ_` settings below:

```jsonc
"quiz": {
  "command": "/opt/signalbot-addons/quiz",
  "chat_commands": {"quiz": {"tool": "quiz", "direct": true, "example": "/quiz 80s music", "timeout": "90s"}},
  "addon": {
    "chats": ["all"],
    "post": true, "watch": true, "react": true,
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
- `post`, for each question, its answer (a reply to the question) and the
  results. The bot drops a post over the chat's post `rate` for good, so the
  quiz asks for no reactions to answers, which take from the same budget;
- `react`, for the ⏰ (😱 on Telegram) on a question with 5 seconds left.
  Without it the bot fails that op, and the quiz plays on without it. With the two posts,
  that's three ops per question: as a question has at least 10 seconds,
  they fit the default rate (20 a minute, bursts of 5);
- `watch`, to read the replies, and the reactions that join a lobby. A quiz
  is at most 10 questions of 2 minutes, never over 30 minutes, and its
  watch allows 15 more for a lobby, so the default `watch_max` of 1h is
  enough.

`"timeout": "90s"` gives the model time to write the quiz where there is no
lobby (a DM, or a bot that doesn't pass reactions). The quiz gives up a few
seconds before the bot's timeout, says so ("The bot's model didn't write
the quiz in time…"), and starts no quiz the bot has given up on. With a
lobby, `/quiz` answers at once and the questions are written meanwhile,
within 2 minutes of their own. The bot takes one sampling request at a
time from an addon, so the quiz writes one quiz at a time: a `/quiz` in a
second chat meanwhile waits for it. A request the quiz gives up on (by
`/quiz end`, say) frees the bot at once, so the next quiz needn't wait;
this needs signalbot 7bb1d03 or newer.

The quiz keeps its own files in `QUIZ_STATE_DIR`. There's nothing to set
up: signalbot sets it to `addons/quiz` in its own state directory
(`/var/lib/signalbot/addons/quiz` under systemd, `/data/addons/quiz` in
Docker), and the quiz creates it, for the bot's user alone. To keep them
elsewhere, set `QUIZ_STATE_DIR` in `/etc/signalbot.env` to a directory the
bot's user may create or write.

It shares the bot's memory limit, so allow for `QUIZ_MEMLIMIT` in
`mem_limit` or `MemoryMax`.

## Settings, in full

signalbot passes an addon server called `quiz` every `QUIZ_` variable from
its environment file, plus `PATH`, `HOME` and the bot's proxy and time zone
settings, and nothing else.

| Variable | Default | Description |
|---|---|---|
| `QUIZ_STATE_DIR` | signalbot's `addons/quiz` | Where the quiz keeps `quiz.json` and its log. Created, mode 700, if missing; the quiz exits at start if it can't create, list and write it |
| `QUIZ_QUESTIONS` | `5` | A quiz's questions when `/quiz` doesn't say, 1 to 10 |
| `QUIZ_TIME` | `35s` | The time for each question when `/quiz` doesn't say, 10s to 2m |
| `QUIZ_LANGUAGE` | `English` | The language the model writes the questions and answers in, by name (letters, spaces and hyphens, up to 40) |
| `QUIZ_DIFFICULTY` | `medium` | `easy`, `medium` or `hard` for every question, or `mixed` (about a third each) |
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
  `QUIZ_DIFFICULTY` (about a third each, for `mixed`); about half of each
  kind; free-text answers of one to three words, with their accepted
  variants and numbers as digits; and
  `QUIZ_LANGUAGE`. The topic is a player's text, and the prompt says to
  treat it as a topic only.
- The quiz shuffles every question's choices, as models put the answer
  first far too often.
- It rejects JSON that isn't strict (once code fences are stripped), a
  question without exactly 4 different choices, an answer that isn't among
  them, a free-text question with no accepted answers, a missing
  explanation or difficulty, a link, and over-long text (a question or an
  explanation over 300 characters, a choice over 80).
- It tells the model the questions the chat had in the last 30 days (the
  newest 60, each with its answer), and asks for none of them or the same
  facts reworded, with a temperature of 1 for variety. It also keeps a hash
  of each, and rejects a question asked there lately.
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
`/quiz`, and during a quiz the replies to its own messages (in a DM, every
message) and, in a lobby, the reactions to them, each with the chat and
sender as pseudonyms and the time the bot got it. It never sees a
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
  points so far; who joined its lobby; and the players questions wait for
  (those who joined, or else who answered question 1). What anyone replied
  is never kept, in memory or on disk;
- 7 days of points per chat, by player pseudonym;
- 30 days of the questions asked in each chat: a hash of each, to refuse
  repeats, and the question with its answer, so the next quiz's prompt can
  tell the model what not to ask again;
- outbox ops the bot hasn't read yet, for at most an hour.

`$QUIZ_STATE_DIR/quiz.log` (moved to `quiz.log.1` past 4 MiB at start)
records quizzes started, lobbies opened, questions written, quizzes begun
after a lobby, called off and ended, with
the number of questions, sampling rounds, rejected questions and repeats,
times, how many joined, questions ended early, and how many players scored.
It never holds a question, an answer, a topic, a prompt, a name, a
pseudonym or message text.

## The protocol

The `addon` package speaks the protocol: the handshake, tool calls and
their cancellation, sampling the bot's model, and the outbox the bot reads.
The quiz adds:

- `quiz` (`{args}`, a direct command), `quiz_answer` (`{text}`, the
  watch's tool) and `quiz_press` (`{press}`, the lobby's buttons'
  `onPress`), all for the bot alone, so none is offered to the model.
- In a group's lobby, on a bot whose capability says `buttons`, the intro's
  result carries one row of at most 4 buttons: ✅ N (Ready), data
  `ready:<id>`, for anyone, then the starter's `t1:<id>` (`Rounds: N`),
  `start:<id>` (▶️, once 2 have joined) and
  `cancel:<id>` (✖️), with `onPress: "quiz_press"`. ✅ joins the presser
  (`_meta` sender) as a reaction does. Each join, leave or setting queues
  an outbox op `{op: "buttons", chat, watch: <id>, buttons}` with the
  new row, replacing the buttons under the intro and keeping its text.
  `wait:<id>`, from an older lobby's ⏱ +30s, answers "That quiz is over.";
  `t2:<id>`, from an older lobby's Answer timer, changes nothing: it
  answers with the time ("Answer timer: 35s") and replaces
  the row with the current one, without it.
  Such a lobby has no countdown, and remembers that across a restart. Its
  watch runs to 15 minutes, plus 2 for writing more questions, plus the
  quiz at the larger number of questions. A press is all in memory, its
  posts and ops queued in the outbox, so it answers at once with the
  pop-up; the lobby's timer and a press take the same lock, so the quiz starts once.
- `/quiz` checks that the bot offered `sampling` and, in the extension's
  capability, `watchReplies` (and `watchReactions`, for a lobby). The
  questions come from one `sampling/createMessage`, charged to the
  command's chat (`_meta["io.github.robdevops.signalbot/chat"]`), and at
  most 2 follow-ups in the same conversation. Without a lobby they are all
  sent before the call's `deadline`, less 3s to reply, and the call
  answers once the quiz is written. In a group's lobby, the call answers at
  once and they are sent outside any tool call, within 2 minutes, stopped
  (with `notifications/cancelled`) if the lobby is called off first.
- It answers with the intro and one watch for the whole quiz:
  `replies: true`, no pattern, `tool: "quiz_answer"`, `reactions: true` in
  a group's lobby, `over: "That quiz is over. Start another with /quiz."`,
  until 10s past the latest the quiz can end (with a lobby, 15 minutes for
  the lobby, during which the questions are written, then the quiz). Reactions come to
  `quiz_answer` as `{reaction, removed}`. No answer's result asks the bot
  to react, as reactions take from the chat's post rate, and a post over it
  is lost.
- Each question is a `post` of its own. Without a lobby, question 1 is
  queued during the `/quiz` call, so it follows the intro the call answers
  with; after one, it is queued when the lobby starts the quiz: at the end of its
  countdown, or, if the questions aren't written by then, after a post
  saying so, once they are. Questions that can't be written in a lobby
  post what `/quiz` would have answered, then an `unwatch`. When a
  question's time is up (3s after its deadline, for answers still in the
  bot's queue), it queues the answer and explanation, with who got it right
  as `{u_…}` placeholders, as a post with `replyTo` the question's post,
  then, 10 seconds later, the next question as a new post. After the last,
  the results follow as a new post, then an `unwatch`. `/quiz end` (or
  `stop`) answers with the open question's answer and the results itself
  and queues only the `unwatch`; in a lobby it calls the quiz off. A lobby
  called off for want of players posts why, then an `unwatch`. Every post
  stays under the bot's 4000 characters.
- 5 seconds before a question's deadline, it queues
  `{op: "react", chat, post, emoji: "⏰"}` (`"😱"` in a Telegram chat:
  Telegram takes only some emoji as reactions, and the bot posts no
  refused one as a message; the chat's network is the call's `_meta`
  `network`, on a bot on several, else the capability's, kept with the
  quiz in `quiz.json` from when it began), with `post` the op ID of the
  question's own post, so the bot reacts to its message (`react`
  permission; an unknown post fails the op, with no retry). There is none
  for a question that ended early (everyone answered, or `/quiz end`), one
  shorter than 10 seconds, or one whose post the outbox refused (so it has
  no op ID). Every question, question 1 included, is a post the quiz
  queued itself, never the `/quiz` call's answer, so each has its ID.
- Ops the bot hasn't read are saved in `quiz.json`, with their IDs, so they
  survive a restart. On start, quizzes still open get their timers back (the
  ⏰ too, if more than 5 seconds are left), and
  a question whose time passed while the quiz was down is revealed at once;
  the next one gets what is left of the watch, and the quiz ends early if
  too little is. A quiz whose questions were still being written in its
  lobby is called off, with a post saying so, then an `unwatch`: the
  sampling conversation doesn't survive a restart, so it isn't resumed.
