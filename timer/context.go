package timer

import "time"

type Context[T any] struct {
	index    int
	Action   ActionType
	handlers []HandlerFunc[T]

	dur   time.Duration
	key   T
	timer *Timer
	err   error
}

func (c *Context[T]) Next() {
	c.index++
	for c.index < len(c.handlers) {
		if c.handlers[c.index] != nil {
			c.handlers[c.index](c)
		}
		c.index++
	}
}

func (c *Context[T]) reset() {
	*c = Context[T]{}
}
