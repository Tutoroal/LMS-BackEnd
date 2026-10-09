package main

import (
	"crypto/sha256"
	"fmt"
	"github.com/gin-gonic/gin"
	"sync"
	"time"
)

type loginFailures struct {
	Count   int
	Expires time.Time
}
type loginGuard struct {
	mutex    sync.Mutex
	failures map[string]loginFailures
}

func newLoginGuard() *loginGuard { return &loginGuard{failures: map[string]loginFailures{}} }
func (guard *loginGuard) key(c *gin.Context, identifier string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(c.ClientIP()+"\x00"+identifier)))
}
func (guard *loginGuard) blocked(key string) bool {
	guard.mutex.Lock()
	defer guard.mutex.Unlock()
	now := time.Now()
	for entry, value := range guard.failures {
		if now.After(value.Expires) {
			delete(guard.failures, entry)
		}
	}
	value := guard.failures[key]
	return value.Count >= 8 || len(guard.failures) >= 10000
}
func (guard *loginGuard) record(key string, success bool) {
	guard.mutex.Lock()
	defer guard.mutex.Unlock()
	if success {
		delete(guard.failures, key)
		return
	}
	value := guard.failures[key]
	if value.Expires.IsZero() || time.Now().After(value.Expires) {
		value = loginFailures{Expires: time.Now().Add(15 * time.Minute)}
	}
	value.Count++
	guard.failures[key] = value
}
