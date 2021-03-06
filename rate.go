package rate

import (
	"fmt"
	"time"

	redis "gopkg.in/redis.v4"

	timerate "golang.org/x/time/rate"
)

const redisPrefix = "rate"

type rediser interface {
	Pipelined(func(pipe *redis.Pipeline) error) ([]redis.Cmder, error)
}

//Limiter type
type Limiter struct {
	fallbackLimiter *timerate.Limiter
	redis           rediser
}

// NewLimiter creates a limiter that controls how frequently events
// are allowed to happen. It uses redis to store data and fallbacks
// to the fallbackLimiter when Redis Server is not available.
func NewLimiter(redis rediser, fallbackLimiter *timerate.Limiter) *Limiter {
	return &Limiter{
		fallbackLimiter: fallbackLimiter,
		redis:           redis,
	}
}

// AllowN reports whether an event with given name may happen at time now.
// It allows up to maxn events within duration dur, with each interaction incrementing
// the limit by n.
func (l *Limiter) AllowN(name string, maxn int64, dur time.Duration, n int64) (count, reset int64, allow bool) {
	udur := int64(dur / time.Second)
	slot := time.Now().Unix() / udur
	reset = (slot + 1) * udur
	allow = l.fallbackLimiter.Allow()

	name = fmt.Sprintf("%s:%s-%d", redisPrefix, name, slot)
	count, err := l.incr(name, dur, n)
	if err == nil {
		allow = count <= maxn
	}

	return count, reset, allow
}

// Allow reports whether an event with given name may happen at time now.
// It allows up to maxn events within duration dur.
func (l *Limiter) Allow(name string, maxn int64, dur time.Duration) (count, reset int64, allow bool) {
	return l.AllowN(name, maxn, dur, 1)
}

// AllowMinute is shorthand for Allow(name, maxn, time.Minute).
func (l *Limiter) AllowMinute(name string, maxn int64) (int64, int64, bool) {
	return l.Allow(name, maxn, time.Minute)
}

// AllowHour is shorthand for Allow(name, maxn, time.Hour).
func (l *Limiter) AllowHour(name string, maxn int64) (int64, int64, bool) {
	return l.Allow(name, maxn, time.Hour)
}

// AllowRate reports whether an event may happen at time now.
// It allows up to rateLimit events each second.
func (l *Limiter) AllowRate(name string, rateLimit timerate.Limit) (delay time.Duration, allow bool) {
	if rateLimit == 0 {
		return 0, false
	}
	if rateLimit == timerate.Inf {
		return 0, true
	}

	dur := time.Second
	limit := int64(rateLimit)
	if limit == 0 {
		limit = 1
		dur *= time.Duration(1 / rateLimit)
	}

	now := time.Now()
	slot := now.UnixNano() / dur.Nanoseconds()
	allow = l.fallbackLimiter.Allow()

	name = fmt.Sprintf("%s:%s-%d-%d", redisPrefix, name, dur, slot)
	count, err := l.incr(name, dur, 1)
	if err == nil {
		allow = count <= limit
	}

	if !allow {
		delay = time.Duration(slot+1)*dur - time.Duration(now.UnixNano())
	}

	return delay, allow
}

func (l *Limiter) incr(name string, dur time.Duration, n int64) (int64, error) {
	var incr *redis.IntCmd
	_, err := l.redis.Pipelined(func(pipe *redis.Pipeline) error {
		incr = pipe.IncrBy(name, n)
		pipe.Expire(name, dur)
		return nil
	})

	rate, _ := incr.Result()
	return rate, err
}
