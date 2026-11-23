package main

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// config is the quiz's settings, every one from a QUIZ_ variable, the only
// ones signalbot passes an addon called quiz.
type config struct {
	StateDir   string        // QUIZ_STATE_DIR: where quiz.json and the log are kept
	Questions  int           // QUIZ_QUESTIONS: a quiz's default number of questions
	Time       time.Duration // QUIZ_TIME: the default time for each question
	Language   string        // QUIZ_LANGUAGE: what the questions and answers are written in
	Difficulty string        // QUIZ_DIFFICULTY: medium (the default), mixed, easy or hard
	MaxTokens  int           // QUIZ_MAX_TOKENS: the most the model may write for one quiz
	MemLimit   int64         // QUIZ_MEMLIMIT, in bytes
	gap        time.Duration // between a question's answer and the next: gapTime, not a setting; 0 in most tests
}

// The limits of a quiz, and the defaults of the settings.
const (
	minQuestions     = 1
	maxQuestions     = 10
	defaultQuestions = 5
	minTime          = 10 * time.Second
	maxTime          = 2 * time.Minute
	defaultTime      = 40 * time.Second
	// maxQuiz is the longest a whole quiz may run, to fit well within the
	// bot's watch_max (1h by default).
	maxQuiz          = 30 * time.Minute
	defaultLanguage  = "English"
	defaultMaxTokens = 3000
	minMaxTokens     = 500
	maxMaxTokens     = 32000
	defaultMemLimit  = "64MiB"
	maxTopic         = 60 // runes
)

// difficulties are QUIZ_DIFFICULTY's values.
var difficulties = []string{"mixed", "easy", "medium", "hard"}

// loadConfig reads the settings through getenv (os.Getenv), failing with a
// message that names the setting.
func loadConfig(getenv func(string) string) (config, error) {
	c := config{
		StateDir:   strings.TrimSpace(getenv("QUIZ_STATE_DIR")),
		Questions:  defaultQuestions,
		Time:       defaultTime,
		Language:   cmp.Or(strings.TrimSpace(getenv("QUIZ_LANGUAGE")), defaultLanguage),
		Difficulty: cmp.Or(strings.ToLower(strings.TrimSpace(getenv("QUIZ_DIFFICULTY"))), "medium"),
		MaxTokens:  defaultMaxTokens,
		gap:        gapTime,
	}
	var errs []error
	if c.StateDir == "" {
		errs = append(errs, errors.New("QUIZ_STATE_DIR is not set: signalbot sets it for an addon called quiz from its own state directory, or set it to a directory the quiz may create or write"))
	}
	var err error
	if c.MemLimit, err = parseSize(cmp.Or(strings.TrimSpace(getenv("QUIZ_MEMLIMIT")), defaultMemLimit)); err != nil {
		errs = append(errs, fmt.Errorf("QUIZ_MEMLIMIT: %v", err))
	}
	if v := strings.TrimSpace(getenv("QUIZ_QUESTIONS")); v != "" {
		if c.Questions, err = questionCount(v); err != nil {
			errs = append(errs, fmt.Errorf("QUIZ_QUESTIONS: %v", err))
		}
	}
	if v := strings.TrimSpace(getenv("QUIZ_TIME")); v != "" {
		if c.Time, err = questionTime(v); err != nil {
			errs = append(errs, fmt.Errorf("QUIZ_TIME: %v", err))
		}
	}
	if !validLanguage(c.Language) {
		errs = append(errs, fmt.Errorf("QUIZ_LANGUAGE is %q: use a language's name, such as English or Deutsch", clamp(c.Language, 40)))
	}
	if !slices.Contains(difficulties, c.Difficulty) {
		errs = append(errs, fmt.Errorf("QUIZ_DIFFICULTY is %q: use mixed, easy, medium or hard", clamp(c.Difficulty, 40)))
	}
	if v := strings.TrimSpace(getenv("QUIZ_MAX_TOKENS")); v != "" {
		if c.MaxTokens, err = strconv.Atoi(v); err != nil || c.MaxTokens < minMaxTokens || c.MaxTokens > maxMaxTokens {
			errs = append(errs, fmt.Errorf("QUIZ_MAX_TOKENS is %q: use a whole number from %d to %d", clamp(v, 40), minMaxTokens, maxMaxTokens))
		}
	}
	return c, errors.Join(errs...)
}

// questionCount reads a number of questions, 1 to 10.
func questionCount(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < minQuestions || n > maxQuestions {
		return 0, fmt.Errorf("%q isn't a number of questions: use 1 to 10", clamp(s, 40))
	}
	return n, nil
}

// questionTime reads the time for each question, such as 45s or 2m, from
// 10s to 2m.
func questionTime(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil || d < minTime || d > maxTime {
		return 0, fmt.Errorf("%q isn't a time for each question: use 10s to 2m, such as 45s", clamp(s, 40))
	}
	return d, nil
}

// validLanguage reports whether s looks like a language's name: letters,
// spaces and hyphens, up to 40, so it can't carry instructions into the
// prompt.
func validLanguage(s string) bool {
	if s == "" || len([]rune(s)) > 40 {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && r != ' ' && r != '-' {
			return false
		}
	}
	return true
}

// sizeUnits are the suffixes parseSize takes, as GOMEMLIMIT does.
var sizeUnits = []struct {
	suffix string
	mult   int64
}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"B", 1}}

// parseSize reads a number of bytes written as GOMEMLIMIT is, such as
// 64MiB or 1GiB.
func parseSize(s string) (int64, error) {
	mult := int64(1)
	num := s
	for _, u := range sizeUnits {
		if n, ok := strings.CutSuffix(s, u.suffix); ok {
			num, mult = n, u.mult
			break
		}
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
	if err != nil || n < 0 || n*float64(mult) >= math.MaxInt64 {
		return 0, fmt.Errorf("%q isn't a size: use a number of bytes with B, KiB, MiB or GiB, such as 64MiB", s)
	}
	if n*float64(mult) < 16<<20 {
		return 0, fmt.Errorf("%q is too small: the quiz needs at least 16MiB", s)
	}
	return int64(n * float64(mult)), nil
}

// checkDir creates dir, for the quiz's user alone, if it isn't there, and
// fails unless it can be listed and written, leaving nothing.
func checkDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("QUIZ_STATE_DIR %s can't be created (%v): create it, writable by the bot's user", dir, err)
	}
	if _, err := os.ReadDir(dir); err != nil {
		return fmt.Errorf("QUIZ_STATE_DIR %s can't be read (%v): make it readable by the bot's user", dir, err)
	}
	f, err := os.CreateTemp(dir, ".check-*")
	if err == nil {
		f.Close()
		err = os.Remove(f.Name())
	}
	if err != nil {
		return fmt.Errorf("QUIZ_STATE_DIR %s can't be written (%v): make it writable by the bot's user", dir, err)
	}
	return nil
}

// clamp cuts s to max runes, marking a cut with "…".
func clamp(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}
