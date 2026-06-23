package timer

import "time"

type Context[T any] struct {
	index    int
	action   ActionType
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

func (c *Context[T]) Action() ActionType { return c.action }
func (c *Context[T]) Key() T             { return c.key }
func (c *Context[T]) Err() error         { return c.err }

func (c *Context[T]) reset() {
	*c = Context[T]{}
}
