package vcs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const DefaultReadTimeout = 30 * time.Second

func ParseReadTimeout(value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil || d < time.Second || d > time.Hour {
		return 0, fmt.Errorf("укажите таймаут чтения от 1s до 1h, например 60s")
	}
	return d, nil
}

func ReadTimeoutPath(home string) string {
	return filepath.Join(home, "read-timeout")
}

func ReadTimeout(home string) (time.Duration, error) {
	path := ReadTimeoutPath(home)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultReadTimeout, nil
	}
	if err != nil {
		return 0, err
	}
	return ParseReadTimeout(strings.TrimSpace(string(data)))
}

type readTimeoutKey struct{}

func WithReadTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	next := min(2*timeout, time.Hour)
	message := fmt.Sprintf("Время чтения репозитория истекло (%s). Чтобы увеличить лимит, выполните `duet config read-timeout %s` и повторите проверку.", timeout, next)
	if next == timeout {
		message = fmt.Sprintf("Время чтения репозитория истекло (%s). Достигнут максимальный таймаут чтения.", timeout)
	}
	cause := errors.New(message)
	return context.WithTimeout(context.WithValue(ctx, readTimeoutKey{}, cause), timeout)
}

// ReadTimeoutError supplies the configured limit for both host and plugin deadlines.
func ReadTimeoutError(ctx context.Context, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
		if info, ok := ctx.Value(readTimeoutKey{}).(error); ok {
			return info
		}
	}
	return err
}
