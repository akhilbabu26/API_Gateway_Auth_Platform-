// Package ratelimit provides a small Redis-backed fixed-window limiter.
package ratelimit

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Redis stores request counters in Redis so limits are shared by service replicas.
// Each operation uses one short-lived connection; this keeps the package dependency-free.
type Redis struct {
	address  string
	password string
	database string
	timeout  time.Duration
}

func NewRedis(redisURL string, timeout time.Duration) (*Redis, error) {
	u, err := url.Parse(redisURL)
	if err != nil || u.Scheme != "redis" || u.Host == "" {
		return nil, fmt.Errorf("invalid Redis URL")
	}
	db := strings.TrimPrefix(u.Path, "/")
	if db == "" {
		db = "0"
	}
	if _, err := strconv.Atoi(db); err != nil {
		return nil, fmt.Errorf("invalid Redis database")
	}
	if timeout <= 0 {
		timeout = 250 * time.Millisecond
	}
	password := ""
	if u.User != nil {
		password, _ = u.User.Password()
	}
	return &Redis{address: u.Host, password: password, database: db, timeout: timeout}, nil
}

const incrementScript = `local n=redis.call('INCR',KEYS[1]); if n==1 then redis.call('PEXPIRE',KEYS[1],ARGV[1]) end; local ttl=redis.call('PTTL',KEYS[1]); return {n,ttl}`

// Allow increments the key atomically and returns whether this request is within limit,
// the remaining request count, and the retry delay.
func (r *Redis) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Duration, error) {
	if limit < 1 || window <= 0 {
		return false, 0, 0, fmt.Errorf("limit and window must be positive")
	}
	dialer := net.Dialer{Timeout: r.timeout}
	conn, err := dialer.DialContext(ctx, "tcp", r.address)
	if err != nil {
		return false, 0, 0, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(r.timeout))
	reader := bufio.NewReader(conn)
	if r.password != "" {
		if _, err = command(conn, reader, "AUTH", r.password); err != nil {
			return false, 0, 0, err
		}
	}
	if r.database != "0" {
		if _, err = command(conn, reader, "SELECT", r.database); err != nil {
			return false, 0, 0, err
		}
	}
	value, err := command(conn, reader, "EVAL", incrementScript, "1", key, strconv.FormatInt(window.Milliseconds(), 10))
	if err != nil {
		return false, 0, 0, err
	}
	values, ok := value.([]interface{})
	if !ok || len(values) != 2 {
		return false, 0, 0, fmt.Errorf("unexpected Redis response")
	}
	count, ok1 := values[0].(int64)
	ttl, ok2 := values[1].(int64)
	if !ok1 || !ok2 {
		return false, 0, 0, fmt.Errorf("unexpected Redis counter response")
	}
	if ttl < 0 {
		ttl = window.Milliseconds()
	}
	remaining := limit - int(count)
	if remaining < 0 {
		remaining = 0
	}
	return count <= int64(limit), remaining, time.Duration(ttl) * time.Millisecond, nil
}

func command(conn net.Conn, reader *bufio.Reader, args ...string) (interface{}, error) {
	if _, err := fmt.Fprintf(conn, "*%d\r\n", len(args)); err != nil {
		return nil, err
	}
	for _, arg := range args {
		if _, err := fmt.Fprintf(conn, "$%d\r\n%s\r\n", len(arg), arg); err != nil {
			return nil, err
		}
	}
	return readRESP(reader)
}

func readRESP(r *bufio.Reader) (interface{}, error) {
	prefix, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	switch prefix {
	case '+':
		return line, nil
	case '-':
		return nil, fmt.Errorf("Redis: %s", line)
	case ':':
		return strconv.ParseInt(line, 10, 64)
	case '*':
		n, err := strconv.Atoi(line)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("invalid Redis array")
		}
		items := make([]interface{}, n)
		for i := range items {
			items[i], err = readRESP(r)
			if err != nil {
				return nil, err
			}
		}
		return items, nil
	case '$':
		n, err := strconv.Atoi(line)
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, nil
		}
		data := make([]byte, n+2)
		if _, err := io.ReadFull(r, data); err != nil {
			return nil, err
		}
		return string(data[:n]), nil
	default:
		return nil, fmt.Errorf("unsupported Redis response")
	}
}
