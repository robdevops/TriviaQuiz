// Command quiz is a trivia quiz for signalbot: /quiz has the bot's own
// model write a few questions on a topic, players answer each by replying
// to the quiz's messages, and when a question's time is up the bot gives
// the answer, who got it right, and the next question.
//
// It is an MCP server on stdio (protocol 2025-06-18, newline-delimited
// JSON-RPC) that speaks signalbot's addon extension: signalbot starts it as
// a child process, calls its tools for /quiz and for each reply its watch
// takes, answers its sampling requests with the bot's model, and reads its
// outbox for the answers and results. Stdout carries nothing but the
// protocol. It keeps its state and its own log in QUIZ_STATE_DIR; the log
// never holds a question, an answer, a topic, a name or message text.
//
// Every setting is a QUIZ_ variable, which signalbot passes to an addon
// server called quiz; see README.md.
package main

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
)

// maxLog is the size past which the log is moved aside at start, keeping
// one old log.
const maxLog = 4 << 20

func main() { os.Exit(run()) }

func run() int {
	// Stdout is the protocol's: anything else printed by mistake goes to
	// stderr, which signalbot counts but never logs.
	stdout := os.Stdout
	os.Stdout = os.Stderr
	cfg, err := loadConfig(os.Getenv)
	if err == nil {
		err = checkDir(cfg.StateDir)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "quiz: %v\n", err)
		return 1
	}
	debug.SetMemoryLimit(cfg.MemLimit)
	logger, closeLog, err := openLog(cfg.StateDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "quiz: %v\n", err)
		return 1
	}
	defer closeLog()
	s, err := newServer(cfg, realClock{}, rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())), logger)
	if err != nil {
		logger.Printf("can't start: %v", err)
		fmt.Fprintf(os.Stderr, "quiz: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- s.srv.Run(ctx, os.Stdin, stdout) }()
	select {
	case err = <-done:
	case <-ctx.Done():
	}
	s.save()
	if err != nil {
		logger.Printf("stopped: %v", err)
		return 1
	}
	logger.Printf("stopped")
	return 0
}

// openLog opens the quiz's own log, quiz.log in dir, moving a large one
// aside to quiz.log.1 first.
func openLog(dir string) (*log.Logger, func(), error) {
	path := filepath.Join(dir, "quiz.log")
	if st, err := os.Stat(path); err == nil && st.Size() > maxLog {
		os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("can't open its log in QUIZ_STATE_DIR: %v", err)
	}
	return log.New(f, "", log.LstdFlags), func() { f.Close() }, nil
}
