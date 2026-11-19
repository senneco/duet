package core

import (
	"errors"
	"sync"
)

// FreezeForUpdate проверяет занятость и удерживает состояние неизменным до
// остановки процесса или отмены обновления. Проверка и остановка записей атомарны.
func (c *Core) FreezeForUpdate() (func(), error) {
	c.mu.Lock()
	for _, r := range c.s.Reviews {
		if r.Working || r.Busy || r.Stopping || r.Editing || c.count(r, CommentWaiting, CommentWorking) != 0 {
			c.mu.Unlock()
			return nil, errors.New("finish agent work and save open edits before applying the update")
		}
	}
	return sync.OnceFunc(c.mu.Unlock), nil
}
