# Quiz

A trivia quiz for [signalbot](https://github.com/robdevops/signalbot).
`/quiz` has the bot's own model write a few questions on a topic. Some are
multiple choice (A–D), some are answered in a word or two. Players reply to
the quiz's messages, the bot reveals each answer, and at the end it ranks
the players and keeps a 7-day scoreboard.

```
/quiz                      a quiz of general knowledge (QUIZ_QUESTIONS questions of QUIZ_TIME, default 5 of 30s)
/quiz 80s music            a quiz on a topic, up to 60 characters
/quiz 80s music 5 45s      with a number of questions (1 to 10) and a time for each (10s to 2m), at the end, in either order
/quiz end, /quiz stop      finish now: the open question's answer and the results (only the player who started it)
/quiz scores               this chat's totals over the last 7 days
```

## How to play

- **Joining.** In a group, react to the quiz's intro with any emoji to
  join. Whoever starts it must react too. It needs at least 2 players.
  It starts 30 seconds after the last one joins, and is called off if
  fewer than 2 have joined after 5 minutes. On Telegram the bot sees
  reactions only as a group admin.
- **DMs.** There is no lobby: the quiz starts at once. Any message you
  send is an answer.
- **Answering.** In a group, reply to one of the quiz's messages. For
  multiple choice, reply with the letter. For free text, reply with the
  answer: case and small typos don't matter, numbers must be exact.
- **Changing your mind.** Your last answer before time is up counts.
- **Points.** 5, 10 or 15 for an easy, medium or hard question, and 5
  more for the first right answer (marked ⚡).
- **Ending early.** A question ends once everyone who joined has
  answered. The player who started the quiz can finish it with
  `/quiz end`.
- **No repeats.** The quiz avoids questions the chat had in the last 30
  days.
- **After the quiz,** for an hour, a reply to one of its messages gets
  "That quiz is over. Start another with /quiz."

## Quick start

You need signalbot main at `d685491` or newer, with a model set up, and
Go 1.27 or newer.

1. **Build it:**

   ```
   git clone https://github.com/robdevops/chat-plugin-quiz.git
   cd chat-plugin-quiz
   CGO_ENABLED=0 go build -ldflags='-s -w' -o /opt/signalbot-addons/quiz .
   ```

2. **Add it to the MCP servers file** (`MCP_CONFIG`). It must be called
   `quiz`. `chats: ["all"]` plays in every group; list group IDs to limit
   it, and add `"dms": true` to play in DMs too. The `model` permission
   is essential: the bot's model writes the questions.

   ```jsonc
   "quiz": {
     "command": "/opt/signalbot-addons/quiz",
     "chat_commands": {"quiz": {"tool": "quiz", "direct": true, "example": "/quiz 80s music", "timeout": "90s"}},
     "addon": {
       "chats": ["all"],
       "post": true, "watch": true,
       "model": {"per_hour": 30, "max_tokens": 4000}
     }
   }
   ```

3. **Restart signalbot** and type `/quiz` in the group.

Nothing else needs setting. All the settings (such as `QUIZ_DIFFICULTY`
and `QUIZ_LANGUAGE`), the permissions, and the Docker and systemd notes
are in [docs/DETAILS.md](docs/DETAILS.md#settings-in-full).

## Credits and privacy

The questions come from the bot's own model, through MCP sampling. The
topic and the quiz's prompt go to the model. Players' answers do not:
the quiz marks them itself. It never logs names or message text.

More: [docs/DETAILS.md](docs/DETAILS.md)
